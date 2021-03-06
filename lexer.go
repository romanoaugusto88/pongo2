package pongo2

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	TokenError = iota
	EOF

	TokenHTML

	TokenKeyword
	TokenIdentifier
	TokenString
	TokenNumber
	TokenSymbol
)

var (
	tokenSpaceChars      = " \n\r\t"
	tokenIdentifierChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_"
	tokenDigits          = "0123456789"
	tokenSymbols         = []string{
		// 3-Char symbols

		// 2-Char symbols
		"==", ">=", "<=", "&&", "||", "{{", "}}", "{%", "%}", "!=", "<>",

		// 1-Char symbol
		"(", ")", "+", "-", "*", "<", ">", "/", "^", ",", ".", "!", "|", ":", "=",
	}
	tokenKeywords = []string{"in", "and", "or", "not", "true", "false"}
)

type TokenType int
type Token struct {
	Typ  TokenType
	Val  string
	Line int
	Col  int
}

type lexerStateFn func() lexerStateFn
type lexer struct {
	name      string
	input     string
	start     int // start pos of the item
	pos       int // current pos
	width     int // width of last rune
	tokens    []*Token
	errored   bool
	startline int
	startcol  int
	line      int
	col       int
}

func (t *Token) String() string {
	val := t.Val
	if len(val) > 1000 {
		val = fmt.Sprintf("%s...%s", val[:10], val[len(val)-5:len(val)])
	}

	typ := ""
	switch t.Typ {
	case TokenHTML:
		typ = "HTML"
	case TokenError:
		typ = "Error"
	case TokenIdentifier:
		typ = "Identifier"
	case TokenKeyword:
		typ = "Keyword"
	case TokenNumber:
		typ = "Number"
	case TokenString:
		typ = "String"
	case TokenSymbol:
		typ = "Symbol"
	default:
		typ = "Unknown"
	}

	return fmt.Sprintf("<Token typ=%s (%d) val='%s'>", typ, t.Typ, val)
}

func lex(name string, input string) ([]*Token, error) {
	l := &lexer{
		name:      name,
		input:     input,
		tokens:    make([]*Token, 0, 100),
		line:      1,
		col:       1,
		startline: 1,
		startcol:  1,
	}
	l.run()
	if l.errored {
		errtoken := l.tokens[len(l.tokens)-1]
		return nil, errors.New(fmt.Sprintf("[Lexer Error in %s (Line %d Col %d)]: %s",
			name, errtoken.Line, errtoken.Col, errtoken.Val))
	}
	return l.tokens, nil
}

func (l *lexer) value() string {
	return l.input[l.start:l.pos]
}

func (l *lexer) emit(t TokenType) {
	tok := &Token{
		Typ:  t,
		Val:  l.value(),
		Line: l.startline,
		Col:  l.startcol,
	}
	l.tokens = append(l.tokens, tok)
	l.start = l.pos
	l.startline = l.line
	l.startcol = l.col
}

func (l *lexer) next() rune {
	if l.pos >= len(l.input) {
		l.width = 0
		return EOF
	}
	r, w := utf8.DecodeRuneInString(l.input[l.pos:])
	l.width = w
	l.pos += l.width
	return r
}

func (l *lexer) backup() {
	l.pos -= l.width
}

func (l *lexer) peek() rune {
	r := l.next()
	l.backup()
	return r
}

func (l *lexer) ignore() {
	l.start = l.pos
}

func (l *lexer) accept(what string) bool {
	if strings.IndexRune(what, l.next()) >= 0 {
		return true
	}
	l.backup()
	return false
}

func (l *lexer) acceptRun(what string) {
	for strings.IndexRune(what, l.next()) >= 0 {
	}
	l.backup()
}

func (l *lexer) errorf(format string, args ...interface{}) lexerStateFn {
	t := &Token{
		Typ:  TokenError,
		Val:  fmt.Sprintf(format, args...),
		Line: l.startline,
		Col:  l.startcol,
	}
	l.tokens = append(l.tokens, t)
	l.errored = true
	l.startline = l.line
	l.startcol = l.col
	return nil
}

func (l *lexer) eof() bool {
	return l.start >= len(l.input)-1
}

func (l *lexer) run() {
	for {
		// Ignore single-line comments {# ... #}
		if strings.HasPrefix(l.input[l.pos:], "{#") {
			if l.pos > l.start {
				l.emit(TokenHTML)
			}

			l.pos += 2 // pass '{#'
			l.col += 2

			for {
				switch l.peek() {
				case EOF:
					l.errorf("Single-line comment not closed.")
					return
				case '\n':
					l.errorf("Newline not permitted in a single-line comment.")
					return
				}

				if strings.HasPrefix(l.input[l.pos:], "#}") {
					l.pos += 2 // pass '#}'
					l.col += 2
					break
				}
				l.pos++
				l.col++
			}
			l.ignore() // ignore whole comment

			// Comment skipped
			continue // next token
		}

		if strings.HasPrefix(l.input[l.pos:], "{{") || // variable
			strings.HasPrefix(l.input[l.pos:], "{%") { // tag
			if l.pos > l.start {
				l.emit(TokenHTML)
			}
			l.tokenize()
			if l.errored {
				return
			}
		} else {
			switch l.peek() {
			case '\n':
				l.line++
				l.col = 1
			default:
				l.col++
			}
			if l.next() == EOF {
				break
			}
		}
	}

	if l.pos > l.start {
		l.emit(TokenHTML)
	}
}

func (l *lexer) tokenize() {
	for state := l.stateCode; state != nil; {
		state = state()
	}
}

func (l *lexer) stateCode() lexerStateFn {
outer_loop:
	for {
		switch {
		case l.accept(tokenSpaceChars):
			if l.value() == "\n" {
				l.line++
				l.col = 1
			} else {
				l.col++
			}
			l.ignore()
			continue
		case l.accept(tokenIdentifierChars):
			return l.stateIdentifier
		case l.accept(tokenDigits):
			return l.stateNumber
		case l.accept(`"`):
			return l.stateString
		}

		// Check for symbol
		for _, sym := range tokenSymbols {
			if strings.HasPrefix(l.input[l.start:], sym) {
				l.pos += len(sym)
				l.col += len(sym)
				l.emit(TokenSymbol)

				if sym == "%}" || sym == "}}" {
					// Tag/variable end, return after emit
					return nil
				}

				continue outer_loop
			}
		}

		if l.pos < len(l.input) {
			return l.errorf("Unknown character: %q (%d)", l.peek(), l.peek())
		}

		break
	}

	// Normal shut down
	return nil
}

func (l *lexer) stateIdentifier() lexerStateFn {
	l.acceptRun(tokenIdentifierChars)
	l.acceptRun(tokenDigits)
	for _, kw := range tokenKeywords {
		if kw == l.value() {
			l.emit(TokenKeyword)
			return l.stateCode
		}
	}
	l.col += len(l.value())
	l.emit(TokenIdentifier)
	return l.stateCode
}

func (l *lexer) stateNumber() lexerStateFn {
	l.acceptRun(tokenDigits)
	/*
		Maybe context-sensitive number lexing?
		* comments.0.Text // first comment
		* usercomments.1.0 // second user, first comment
		* if (score >= 8.5) // 8.5 as a number

		if l.peek() == '.' {
			l.accept(".")
			if !l.accept(tokenDigits) {
				return l.errorf("Malformed number.")
			}
			l.acceptRun(tokenDigits)
		}
	*/
	l.col += len(l.value())
	l.emit(TokenNumber)
	return l.stateCode
}

func (l *lexer) stateString() lexerStateFn {
	l.ignore()
	for !l.accept(`"`) {
		if l.next() == EOF {
			return l.errorf("Unexpected EOF, string not closed.")
		}
	}
	l.backup()
	l.col += len(l.value())
	l.emit(TokenString)
	l.next()
	l.ignore()
	return l.stateCode
}

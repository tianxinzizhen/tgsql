package preparse

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type sqlLexer struct {
	input      string // the string being scanned
	pos        Pos    // current position in the input
	start      Pos    // start position of this item
	atEOF      bool   // we have hit the end of input and returned eof
	item       item
	leftDelim  string // start of action marker
	rightDelim string // end of action marker
}

type Pos int

const (
	itemEOF = iota
	itemLeftDelim
	itemIdentifier
	itemChar
	itemField
	itemLeftParen
	itemRightParen
	itemSpace
	itemRightDelim
	itemString
	itemRawString
	itemError
	itemCharConstant
	itemBool
	itemAtSign
	itemLeftSquareBrackets
	itemRightSquareBrackets

	itemKeyword  // used only to delimit the keywords
	itemBlock    // block keyword
	itemBreak    // break keyword
	itemContinue // continue keyword
	itemDot      // the cursor, spelled '.'
	itemDefine   // define keyword
	itemElse     // else keyword
	itemEnd      // end keyword
	itemIf       // if keyword
	itemNil      // the untyped nil constant, easiest to treat as a keyword
	itemRange    // range keyword
	itemTemplate // template keyword
	itemWith     // with keyword
)

var key = map[string]itemType{
	".":        itemDot,
	"block":    itemBlock,
	"break":    itemBreak,
	"continue": itemContinue,
	"define":   itemDefine,
	"else":     itemElse,
	"end":      itemEnd,
	"if":       itemIf,
	"range":    itemRange,
	"nil":      itemNil,
	"template": itemTemplate,
	"with":     itemWith,
}

type itemType int

type item struct {
	typ  itemType // The type of this item.
	pos  Pos      // The starting position, in bytes, of this item in the input string.
	val  string   // The value of this item.
	line int      // The line number at the start of this item.
}

const eof = -1

func (l *sqlLexer) next() rune {
	if int(l.pos) >= len(l.input) {
		l.atEOF = true
		return eof
	}
	r, w := utf8.DecodeRuneInString(l.input[l.pos:])
	l.pos += Pos(w)
	return r
}

func (l *sqlLexer) peek() rune {
	r := l.next()
	l.backup()
	return r
}

func (l *sqlLexer) backup() {
	if !l.atEOF && l.pos > 0 {
		_, w := utf8.DecodeLastRuneInString(l.input[:l.pos])
		l.pos -= Pos(w)
	}
}

func (l *sqlLexer) thisItem(t itemType) item {
	i := item{typ: t, pos: l.start, val: l.input[l.start:l.pos]}
	l.start = l.pos
	return i
}

func (l *sqlLexer) emit(t itemType) stateSqlFn {
	return l.emitItem(l.thisItem(t))
}

// emitItem passes the specified item to the parser.
func (l *sqlLexer) emitItem(i item) stateSqlFn {
	l.item = i
	return nil
}

type stateSqlFn func(*sqlLexer) stateSqlFn

func (l *sqlLexer) nextItem() item {
	l.item = item{typ: itemEOF, pos: l.pos, val: "EOF"}
	state := lexSql
	for {
		state = state(l)
		if state == nil {
			return l.item
		}
	}
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n'
}

func isAlphaNumeric(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// scanString 扫描单引号或双引号包裹的字符串，处理转义
func (l *sqlLexer) scanString(quote rune) {
	for {
		r := l.next()
		if r == eof {
			return
		}
		if r == '\\' {
			// 跳过转义字符
			l.next()
			continue
		}
		if r == quote {
			// 处理SQL中的双引号转义: '' -> '
			if quote == '\'' && l.peek() == '\'' {
				l.next()
				continue
			}
			return
		}
	}
}

// scanRawString 扫描反引号包裹的原始字符串
func (l *sqlLexer) scanRawString() {
	for {
		r := l.next()
		if r == eof || r == '`' {
			return
		}
	}
}

// lexSql 是词法分析的起始状态
func lexSql(l *sqlLexer) stateSqlFn {
	if strings.HasPrefix(l.input[l.pos:], l.leftDelim) {
		return l.emit(itemLeftDelim)
	}
	if strings.HasPrefix(l.input[l.pos:], l.rightDelim) {
		return l.emit(itemRightDelim)
	}
	switch r := l.next(); {
	case r == eof:
		return l.emit(itemEOF)
	case isSpace(r):
		for isSpace(l.peek()) {
			l.next()
		}
		return l.emit(itemSpace)
	case r == '"':
		l.scanString('"')
		return l.emit(itemString)
	case r == '`':
		l.scanRawString()
		return l.emit(itemRawString)
	case r == '\'':
		l.scanString('\'')
		return l.emit(itemString)
	case r == '.':
		return l.emit(itemDot)
	case r == '[':
		return l.emit(itemLeftSquareBrackets)
	case r == ']':
		return l.emit(itemRightSquareBrackets)
	case r == '@':
		return l.emit(itemAtSign)
	case isAlphaNumeric(r):
		for isAlphaNumeric(l.peek()) {
			l.next()
		}
		word := l.input[l.start:l.pos]
		switch {
		case key[word] > itemKeyword:
			item := key[word]
			return l.emit(item)
		case word == "true", word == "false":
			return l.emit(itemBool)
		default:
			return l.emit(itemIdentifier)
		}
	case r == '(':
		return l.emit(itemLeftParen)
	case r == ')':
		return l.emit(itemRightParen)
	case r <= unicode.MaxASCII && unicode.IsPrint(r):
		return l.emit(itemChar)
	default:
		return l.emit(itemChar)
	}
}

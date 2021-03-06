package lang

import (
	"bufio"
	"bytes"
	"io"
	"regexp"
	"unicode"
	"container/list"
	"math/rand"
	"fmt"
	"strconv"
	"strings"
)

/*
	All sorts of constants
 */

var QUOTE *Symbol = InternSymbol("quote")
var THE_VAR *Symbol = InternSymbol("var")
var UNQUOTE *Symbol = InternSymbol("clojure.core", "unquote")
var UNQUOTE_SPLICING *Symbol = InternSymbol("clojure.core", "unqoute-splicing")
var CONCAT *Symbol = InternSymbol("clojure.core", "concat")
var SEQ *Symbol = InternSymbol("clojure.core", "seq")
var LIST *Symbol = InternSymbol("clojure.core", "list")
var APPLY *Symbol = InternSymbol("clojure.core", "apply")
var HASHMAP *Symbol = InternSymbol("clojure.core", "hash-map")
var HASHSET *Symbol = InternSymbol("clojure.core", "hash-set")
var VECTOR *Symbol = InternSymbol("clojure.core", "vector")
var WITH_META *Symbol = InternSymbol("clojure.core", "with-meta")
var META *Symbol = InternSymbol("clojure.core", "meta")
var DEREF *Symbol = InternSymbol("clojure.core", "deref")
var READ_COND *Symbol = InternSymbol("clojure.core", "read-cond")
var READ_COND_SPLICING *Symbol = InternSymbol("clojure.core", "read-cond-splicing")

var UNKNOWN *Keyword = InternKeywordByNsName("unknown")

var macros map[rune]IFn = map[rune]IFn{
	'"':  &StringReader{},
	';':  &CommentReader{},
	'\'': &WrappingReader{sym: QUOTE},
	'@':  &WrappingReader{sym: DEREF},
	'^':  &MetaReader{},
	'`':  &SyntaxQuoteReader{},
	'~':  &UnquoteReader{},
	'(':  &ListReader{},
	')':  &UnmatchedDelimiterReader{},
	'[':  &VectorReader{},
	']':  &UnmatchedDelimiterReader{},
	'{':  &MapReader{},
	'}':  &UnmatchedDelimiterReader{},
	'\\': &RuneReader{},
	'%':  &ArgReader{},
	'#':  &DispatchReader{},
}

var dispatchMacros map[rune]IFn = map[rune]IFn{
	'^':  &MetaReader{},
	'\'': &VarReader{},
	'"':  &RegexReader{},
	'(':  &FnReader{},
	'{':  &SetReader{},
	'=':  &EvalReader{},
	'!':  &CommentReader{},
	'<':  &UnreadableReader{},
	'_':  &DiscardReader{},
	'?':  &ConditionalReader{},
}

var symbolPat *regexp.Regexp = regexp.MustCompile(`:?([^/\d].*/)?(/|[^\d/][^/]*)`)
var intPat *regexp.Regexp = regexp.MustCompile(`([-+]?)(?:(0)|([1-9][0-9]*)|0[xX]([0-9A-Fa-f]+)|0([0-7]+)|([1-9][0-9]?)[rR]([0-9A-Za-z]+)|0[0-9]+)(N)?`)
var radioPat *regexp.Regexp = regexp.MustCompile(`([-+]?[0-9]+)/([0-9]+)`)
var floatPat *regexp.Regexp = regexp.MustCompile("([-+]?[0-9]+(\\.[0-9]*)?([eE][-+]?[0-9]+)?)(M)?")

var GENSYM_ENV *Var = CreateVarFromNothing().SetDynamic()
var ARG_ENV *Var = CreateVarFromNothing().SetDynamic()
var ctorReader IFn = &CtorReader{}

var READ_COND_ENV *Var = CreateVarFromNothing().SetDynamic()

// Reader opts
var OPT_EOF *Keyword = InternKeywordByNsName("eof")
var OPT_FEATURES *Keyword = InternKeywordByNsName("features")
var OPT_READ_COND *Keyword = InternKeywordByNsName("read-cond")

// EOF special value to throw on eof
var EOFTHROW *Keyword = InternKeywordByNsName("eofthrow")

// Platform features - always installed
var PLATFORM_KEY *Keyword = InternKeywordByNsName("clj") // NOTE: "glj" ?
var PLATFORM_FEATURES interface{} = CreatePersistentHashSetFromInterfaceSlice(PLATFORM_KEY)

// Reader conditional options - use with :read-cond
var COND_ALLOW *Keyword = InternKeywordByNsName("allow")
var COND_PRESERVE *Keyword = InternKeywordByNsName("preserve")

// These are sentinel values.
var READ_EOF = rand.Int()
var READ_FINISHED = rand.Int()

// NOTE: isWhiteSpace => unicode.isSpace(ch)

// TODO: A large block of code here

/*
	LispReader

	NOTE: For simplicity, I have created a class to cover a lot of the static reader methods that exist in
	the JVM Clojure equivalent file.
*/

type LispReader struct {
	r *bufio.Reader
}

func (lr *LispReader) ReadRune() (rune, error) {
	ch, _, err := lr.r.ReadRune()
	if err != nil {
		if err == io.EOF {
			return ch, err
		}
		Util.SneakyThrow(err)
	}
	return ch, nil
}

func (lr *LispReader) UnreadRune() {
	err := lr.r.UnreadRune()
	if err != nil {
		Util.SneakyThrow(err)
	}
}

// TODO: make this private in the future?
func CreateLispReader(r io.Reader) *LispReader {
	return &LispReader{
		r: bufio.NewReader(r),
	}
}

func (lr *LispReader) ensurePending(pendingForms interface{}) interface{} {
	if pendingForms == nil {
		return list.New()
	} else {
		return pendingForms
	}
}

func (lr *LispReader) ReadToken(initch rune) string {
	var b bytes.Buffer
	b.WriteRune(initch)

	for {
		ch, err := lr.ReadRune()
		if err != nil || unicode.IsSpace(ch) || lr.IsTerminatingMacro(ch) {
			lr.UnreadRune()
			return b.String()
		}
		b.WriteRune(ch)
	}
}

// TODO
func (lr *LispReader) ReadNumber(initch rune) interface{} {
	var sb bytes.Buffer
	sb.WriteRune(initch)
	for {
		ch, err := lr.ReadRune()
		if err != nil || unicode.IsSpace(ch) || lr.IsMacro(ch) {
			lr.UnreadRune()
			break
		}
		sb.WriteRune(initch)
	}
	s := sb.String()
	n, interr := strconv.ParseInt(s, 10, 64)
	f, flerr := strconv.ParseFloat(s, 64)

	if interr != nil && flerr != nil {
		panic(fmt.Sprintf("Invalid number: %v", s))
	}
	if interr != nil {
		return f
	} else {
		return int(n)
	}
}

// TODO....there's other functions in here

func (lr *LispReader) IsMacro(ch rune) bool {
	// NOTE: This behaves a little differently in the Java version, due to note using a map for `macros`.
	return macros[ch] != nil
}

func (lr *LispReader) IsTerminatingMacro(ch rune) bool {
	return ch != '#' && ch != '\'' && ch != '%' && lr.IsMacro(ch)
}

func (lr *LispReader) ReadDelimitedList(delim rune, isRecursive bool, opts interface{}, pendingForms interface{}) []interface{} {
	// NOTE: There's some code here that checks to see if the reader is a LineNumberingPushbackReader.
	// We don't have such a thing in Go yet but I might create one in the future.
	firstline := -1

	a := make([]interface{}, 0)
	for {
		form := lr.Read(false, READ_EOF, delim, READ_FINISHED, isRecursive, opts, pendingForms)

		if form == READ_EOF {
			if firstline < 0 {
				panic("EOF while reading")
			} else {
				panic("EOF while reading, starting at line " + string(firstline))
			}
		} else if form == READ_FINISHED {
			return a
		}

		a = append(a, form)
	}
}

func (lr *LispReader) Read(eofIsError bool, eofValue interface{}, returnOn rune, returnOnValue interface{}, isRecursive bool, opts interface{}, pendingForms interface{}) interface{} {
	if READEVAL.Deref() == UNKNOWN {
		panic("Reading disallowed - *read-eval* bound to :unknown")
	}

	// TODO: opts = installPlatformFeature(opts)

	for {
		switch pf := pendingForms.(type) {
		case list.List:
			if !(pf.Len() == 0) {
				return pf.Remove(pf.Front())
			}
		}

		ch, err := lr.ReadRune()

		for unicode.IsSpace(ch) {
			ch, err = lr.ReadRune()
		}

		if err == io.EOF {
			if eofIsError {
				panic("EOF while reading")
			}
			return eofValue
		}

		if returnOn != rune(0) && returnOn == ch {
			return returnOnValue
		}

		if unicode.IsDigit(ch) {
			n := lr.ReadNumber(ch)
			return n
		}

		var macroFn IFn = macros[ch]
		if macroFn != nil {

			ret := macroFn.Invoke(lr, ch, opts, pendingForms)

			// NOTE: This doesn't make sense to me.
			if ret == lr.r {
				continue
			}
			return ret
		}

		if ch == '+' || ch == '-' {
			ch2, _ := lr.ReadRune()
			if unicode.IsDigit(ch2) {
				lr.UnreadRune()
				n := lr.ReadNumber(ch)
				return n
			}
			lr.UnreadRune()
		}

		var token string = lr.ReadToken(ch)
		return interpretToken(token)

		// "Catch" in JVM Clojure
		if err != nil {
			if isRecursive {
				Util.SneakyThrow(err)
			}
			panic(err)
		}
	}
}

// TODO: ReaderException

type RegexReader struct {
	AFn
}

func (rr *RegexReader) Invoke(args ...interface{}) interface{} {
	r, _, _, _ := unpackReaderArgs(args)

	var sb bytes.Buffer

	for ch, err := r.ReadRune(); ch != '"'; ch, err = r.ReadRune() {
		if err == io.EOF {
			panic("EOF while reading regex")
		}
		sb.WriteRune(ch)
		if ch == '\\' {
			ch, err = r.ReadRune()
			if err == io.EOF {
				panic("EOF while reading regex")
			}
			sb.WriteRune(ch)
		}
	}
	return regexp.MustCompile(sb.String())
}

type StringReader struct {
	AFn
}

func (sr *StringReader) Invoke(args ...interface{}) interface{} {
	r, _, _, _ := unpackReaderArgs(args)

	var sb bytes.Buffer

	for ch, err := r.ReadRune(); ch != '"'; ch, err = r.ReadRune() {

		if err == io.EOF {
			panic("EOF while reading string")
		}
		if ch == '\\' {
			ch, err = r.ReadRune()
			if err == io.EOF {
				panic("EOF while reading string")

			}
			switch ch {
			case 't':
				ch = '\t'
			case 'r':
				ch = '\r'
			case 'n':
				ch = '\n'
			case '\\':
				break
			case '"':
				break
			case 'b':
				ch = '\b'
			case 'f':
				ch = '\f'
			case 'u':
				ch, err = r.ReadRune()
				if !unicode.IsDigit(ch) {
					// TODO
				}

			/*
				if Character.digit(ch, 16) == -1 {
					panic("Invalid unicode escape") // TODO: flesh this out more
				}
				ch = LispReader.ReadUnicodeChar(r, ch, 16, 4, true)
			*/
			default:
				// TODO
				if unicode.IsDigit(ch) {
					// some stuff
				}
				/*
					if(Character.isDigit(ch)) {
						ch = LispReader.ReadUnicodeChar(r, ch, 8, 3, false);
						if(ch > 0377) {
							panic("Octal escape sequence must be in range [0, 377].");
						} else {}
						panic("Unsupported escape character") // TODO: Flesh this out more
					}
				*/
			}
		}
		sb.WriteRune(ch)
	}
	return sb.String()

}

type CommentReader struct {
	AFn
}

func (cr *CommentReader) Invoke(args ...interface{}) interface{} {
	r, _, _, _:= unpackReaderArgs(args)

	for ch, err := r.ReadRune(); ch != '\n' && ch != '\r' && err != io.EOF; ch, err = r.ReadRune() {
		// Advance the reader through comments
	}
	return r
}

/*
	DiscardReader
 */

type DiscardReader struct {
	AFn
}

func (dr *DiscardReader) Invoke(args ...interface{}) interface{} {
	r, _, opts, pendingForms := unpackReaderArgs(args)
	r.Read(true, nil, rune(0), nil, true, opts, r.ensurePending(pendingForms))
	return r
}

type WrappingReader struct {
	AFn

	sym *Symbol
}

// TODO
func (wr *WrappingReader) Invoke(args ...interface{}) interface{} {
	// reader, quote, opts, pendingForms := unpackReaderArgs(args)
	return nil
}

type VarReader struct {
	AFn
}

func (vr *VarReader) Invoke(args ...interface{}) interface{} {
	r, _, opts, pendingForms := unpackReaderArgs(args)
	o := r.Read(true, nil, rune(0), nil, true, opts, r.ensurePending(pendingForms))
	return RT.List(THE_VAR, o)
}

type DispatchReader struct {
	AFn
}

func (dr *DispatchReader) Invoke(args ...interface{}) interface{} {
	r, _, opts, pendingForms := unpackReaderArgs(args)
	ch, err := r.ReadRune()
	if err == io.EOF {
		panic("EOF while reading character")
	}
	var fn IFn = dispatchMacros[ch]
	if fn == nil {
		r.UnreadRune()
		pendingForms = r.ensurePending(pendingForms)
		result := ctorReader.Invoke(r, ch, opts, pendingForms)

		if result != nil {
			return result
		} else {
			panic("No dispatch macro for: " + string(ch))
		}
	}
	return fn.Invoke(r, ch, opts, pendingForms)
}

type FnReader struct {
	AFn
}

// TODO
func (fr *FnReader) Invoke(args ...interface{}) interface{} {
	return nil
}

type ArgReader struct {
	AFn
}

// TODO
func (ar *ArgReader) Invoke(args ...interface{}) interface{} {
	return nil
}

type MetaReader struct {
	AFn
}

// TODO
func (mr *MetaReader) Invoke(args ...interface{}) interface{} {
	return nil
}

type SyntaxQuoteReader struct {
	AFn
}

// TODO
func (sr *SyntaxQuoteReader) Invoke(args ...interface{}) interface{} {
	return nil
}

type UnquoteReader struct {
	AFn
}

// TODO
func (ur *UnquoteReader) Invoke(args ...interface{}) interface{} {
	return nil
}

/*
	RuneReader [CharacterReader]
*/

type RuneReader struct {
	AFn
}

// TODO
func (cr *RuneReader) Invoke(args ...interface{}) interface{} {
	return nil
}

type ListReader struct {
	AFn
}

func (lr *ListReader) Invoke(args ...interface{}) interface{} {
	r, _, opts, pendingForms := unpackReaderArgs(args)
	line := -1
	column := -1
	l := r.ReadDelimitedList(')', true, opts, r.ensurePending(pendingForms))
	if len(l) == 0 {
		return EMPTY_PERSISTENT_LIST
	}
	s := CreatePersistentListFromInterfaceSlice(l)
	if line != -1 {
		return s.WithMeta(RT.Map(LINE_KEY, line, COLUMN_KEY, column))
	} else {
		return s
	}
}

type EvalReader struct {
	AFn
}

// TODO
func (er *EvalReader) Invoke(args ...interface{}) interface{} {
	return nil
}

type VectorReader struct {
	AFn
}

func (vr *VectorReader) Invoke(args ...interface{}) interface{} {
	r, _, opts, pendingForms := unpackReaderArgs(args)
	return CreateLazilyPersistentVector(r.ReadDelimitedList(']', true, opts, r.ensurePending(pendingForms)))
}

type MapReader struct {
	AFn
}

// TODO
func (mr *MapReader) Invoke(args ...interface{}) interface{} {
	r, _, opts, pendingForms := unpackReaderArgs(args)
	a := r.ReadDelimitedList('}', true, opts, r.ensurePending(pendingForms))
	if len(a) % 2 == 1 {
		panic("Map literal must contain an even number of forms.")
	}
	return RT.Map(a...)
}

type SetReader struct {
	AFn
}

// TODO
func (sr *SetReader) Invoke(args ...interface{}) interface{} {
	return nil
}

type UnmatchedDelimiterReader struct {
	AFn
}

// TODO
func (udr *UnmatchedDelimiterReader) Invoke(args ...interface{}) interface{} {
	return nil
}

type UnreadableReader struct {
	AFn
}

// TODO
func (ur *UnreadableReader) Invoke(args ...interface{}) interface{} {
	return nil
}

type CtorReader struct {
	AFn
}

// TODO
func (cr *CtorReader) Invoke(args ...interface{}) interface{} {
	return nil
}

type ConditionalReader struct {
	AFn
}

// TODO
func (cr *ConditionalReader) Invoke(args ...interface{}) interface{} {
	return nil
}

/*
	Static methods
*/

func unpackReaderArgs(args []interface{}) (*LispReader, interface{}, interface{}, interface{}) {
	a, b, c, d := args[0], args[1], args[2], args[3]
	return a.(*LispReader), b, c, d
}



func interpretToken(s string) interface{} {
	if s == "nil" {
		return nil
	} else if s == "true" {
		return true
	} else if s == "false" {
		return false
	}
	var ret interface{}
	ret = matchSymbol(s)

	if ret != nil {
		return ret
	}
	panic("Invalid token: " + s)
}

// TODO
func matchSymbol(s string) interface{} {
	r := symbolPat.FindString(s)
	if r != "" {
		matches := symbolPat.FindStringSubmatch(s) // maybe I should just do this from the beginning
		var ns string = matches[1]
		var name string = matches[2]
		if (ns != "" && strings.HasSuffix(ns, ":/") || strings.HasSuffix(name, ":") || strings.Index(s[1:], "::") != -1) {
			return nil
		}
		if strings.HasPrefix(s, "::") {
			ks := InternSymbol(s[2:])
			var kns *Namespace
			if ks.ns != "" {
				kns = Compiler.NamespaceFor(Compiler.CurrentNS(), ks)
			} else {
				kns = Compiler.CurrentNS()
			}

			if kns != nil {
				return InternKeywordByNsAndName(kns.name.name, ks.name)
			} else {
				return nil
			}
		}
		var isKeyword bool = strings.Index(s, ":") == 0
		if isKeyword {
			sym := InternSymbol(s[1:])
			return InternKeyword(sym)
		} else {
			return InternSymbol(s[0:])
		}
	}
	return nil
}

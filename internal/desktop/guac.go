package desktop

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The Guacamole protocol is a stream of instructions. Each one is a list of
// elements, each element written as its length, a dot and its value, joined
// by commas and closed by a semicolon:
//
//	4.size,4.1280,3.800;
//
// The length counts Unicode code points, not bytes, which is the one detail
// every implementation has to get right. The first element is the opcode.

// Instruction is one decoded Guacamole instruction.
type Instruction struct {
	Opcode string
	Args   []string
}

// Encode renders an instruction in wire form.
func Encode(opcode string, args ...string) string {
	var b strings.Builder
	writeElement(&b, opcode)
	for _, a := range args {
		b.WriteByte(',')
		writeElement(&b, a)
	}
	b.WriteByte(';')
	return b.String()
}

func writeElement(b *strings.Builder, value string) {
	b.WriteString(strconv.Itoa(utf8.RuneCountInString(value)))
	b.WriteByte('.')
	b.WriteString(value)
}

// maxElementLength bounds a single element. guacd's own limit for a whole
// instruction is far below this; it is here so a corrupt length cannot make
// the reader allocate without end.
const maxElementLength = 8 << 20

// ErrMalformed reports bytes that are not a Guacamole instruction.
var ErrMalformed = errors.New("malformed guacamole instruction")

// Reader decodes instructions from a byte stream.
type Reader struct {
	r *bufio.Reader
}

// NewReader wraps r in an instruction decoder.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, 64<<10)}
}

// Read returns the next whole instruction.
func (r *Reader) Read() (Instruction, error) {
	var elements []string
	for {
		value, last, err := r.element()
		if errors.Is(err, io.EOF) && len(elements) > 0 {
			return Instruction{}, io.ErrUnexpectedEOF
		}
		if err != nil {
			return Instruction{}, err
		}
		elements = append(elements, value)
		if last {
			return Instruction{Opcode: elements[0], Args: elements[1:]}, nil
		}
	}
}

// element reads one length-prefixed element and the terminator after it.
func (r *Reader) element() (string, bool, error) {
	digits, err := r.r.ReadString('.')
	if errors.Is(err, io.EOF) && digits != "" {
		return "", false, io.ErrUnexpectedEOF
	}
	if err != nil {
		return "", false, err
	}
	n, err := strconv.Atoi(digits[:len(digits)-1])
	if err != nil || n < 0 || n > maxElementLength {
		return "", false, fmt.Errorf("%w: length %q", ErrMalformed, digits)
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		ch, _, err := r.r.ReadRune()
		if err != nil {
			return "", false, unexpected(err)
		}
		b.WriteRune(ch)
	}
	term, err := r.r.ReadByte()
	if err != nil {
		return "", false, unexpected(err)
	}
	switch term {
	case ',':
		return b.String(), false, nil
	case ';':
		return b.String(), true, nil
	}
	return "", false, fmt.Errorf("%w: terminator %q", ErrMalformed, term)
}

// unexpected turns a clean EOF inside an element into the error that says the
// stream ended mid-instruction.
func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// Splitter finds instruction boundaries in raw bytes without decoding them.
//
// The relay needs this in the guacd-to-client direction: a WebSocket text
// message must be valid UTF-8, so a frame cannot end in the middle of a
// character, and guacamole-common-js is happiest with whole instructions. The
// splitter walks the stream once, counting code points, and reports how many
// bytes form complete instructions; the rest waits for the next read.
type Splitter struct {
	state     splitState
	length    int // digits of the current length so far
	remaining int // code points left in the current value
	pending   int // bytes of the current partial rune
}

type splitState int

const (
	stateLength splitState = iota
	stateValue
	stateTerminator
)

// Feed advances over buf, which follows directly on the bytes of earlier
// calls, and returns how many of its leading bytes end on an instruction
// boundary. Those bytes, after anything held back from earlier calls, are
// whole instructions; the rest of buf is held back until a later call ends one.
func (s *Splitter) Feed(buf []byte) (int, error) {
	boundary := -1
	for i := 0; i < len(buf); i++ {
		c := buf[i]
		switch s.state {
		case stateLength:
			switch {
			case c >= '0' && c <= '9':
				s.length = s.length*10 + int(c-'0')
				if s.length > maxElementLength {
					return 0, fmt.Errorf("%w: length too large", ErrMalformed)
				}
			case c == '.':
				s.remaining = s.length
				s.length = 0
				s.state = stateValue
				if s.remaining == 0 {
					s.state = stateTerminator
				}
			default:
				return 0, fmt.Errorf("%w: byte %q in length", ErrMalformed, c)
			}
		case stateValue:
			if s.pending > 0 {
				s.pending--
			} else {
				s.pending = runeLen(c) - 1
			}
			if s.pending == 0 {
				s.remaining--
				if s.remaining == 0 {
					s.state = stateTerminator
				}
			}
		case stateTerminator:
			switch c {
			case ',':
				s.state = stateLength
			case ';':
				s.state = stateLength
				boundary = i
			default:
				return 0, fmt.Errorf("%w: terminator %q", ErrMalformed, c)
			}
		}
	}
	return boundary + 1, nil
}

// runeLen is the byte length of the UTF-8 sequence that starts with c. A
// stray continuation byte counts as one, which keeps the count moving on bad
// input instead of stalling; guacd never sends such bytes.
func runeLen(c byte) int {
	switch {
	case c < 0xC0:
		return 1
	case c < 0xE0:
		return 2
	case c < 0xF0:
		return 3
	}
	return 4
}

// ParseAll decodes every whole instruction in s. It is for client frames,
// which guacamole-common-js always sends as whole instructions.
func ParseAll(s string) ([]Instruction, error) {
	r := NewReader(strings.NewReader(s))
	var out []Instruction
	for {
		ins, err := r.Read()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, ins)
	}
}

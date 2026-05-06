package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
)

const (
	keyEnter      = 0x0D // Enter / Return
	keyCtrlJ      = 0x0A // Ctrl+J (newline)
	keyCtrlC      = 0x03 // Ctrl+C
	keyCtrlD      = 0x04 // Ctrl+D
	keyCtrlA      = 0x01 // Ctrl+A
	keyCtrlE      = 0x05 // Ctrl+E
	keyCtrlK      = 0x0B // Ctrl+K
	keyCtrlU      = 0x15 // Ctrl+U
	keyCtrlW      = 0x17 // Ctrl+W
	keyBackspace  = 0x7F // DEL
	keyBackspace2 = 0x08 // BS (some terminals)
	keyEsc        = 0x1B
	keyTab        = 0x09
)

type inputState struct {
	buffer []rune // the full text including \n characters
	cursor int    // position in buffer (0..len(buffer))
	prompt string // "> "
	cont   string // "  " (continuation prefix)
	termW  int    // terminal width in columns

	// tracking for re-render positioning
	lastCurLine int // cursor line position from last render
}

// readMultiline reads multi-line input from stdin in raw mode.
//
//   - Ctrl+J inserts a newline.
//   - Enter submits the text.
//   - Ctrl+C cancels (returns empty string and no error).
//   - Ctrl+D on empty line returns io.EOF.
//
// If stdin is not a terminal, falls back to a single Scanln.
func readMultiline(prompt, cont string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		var line string
		_, err := fmt.Scanln(&line)
		return line, err
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer func() { _ = term.Restore(fd, oldState) }()

	w, _, err := term.GetSize(fd)
	if err != nil || w < 20 {
		w = 80
	}

	s := &inputState{
		prompt: prompt,
		cont:   cont,
		termW:  w,
	}

	// Initial render: print prompt and position cursor after it.
	fmt.Print(s.prompt)
	s.lastCurLine = 0

	var buf [16]byte
	for {
		n, err := os.Stdin.Read(buf[:])
		if err != nil {
			if err == io.EOF {
				return "", io.EOF
			}
			return "", err
		}

		for i := 0; i < n; i++ {
			b := buf[i]

			switch b {
			// ── Submit ──────────────────────────────────────
			case keyEnter:
				fmt.Print("\r\n")
				return string(s.buffer), nil

			// ── Newline ─────────────────────────────────────
			case keyCtrlJ:
				s.insertRune('\n')

			// ── Backspace ───────────────────────────────────
			case keyBackspace, keyBackspace2:
				if s.cursor > 0 {
					s.cursor--
					copy(s.buffer[s.cursor:], s.buffer[s.cursor+1:])
					s.buffer = s.buffer[:len(s.buffer)-1]
				}

			// ── Ctrl shortcuts ─────────────────────────────
			case keyCtrlC:
				// Cancel current input; print ^C and return empty.
				fmt.Print("^C\r\n")
				return "", nil

			case keyCtrlD:
				if len(s.buffer) == 0 {
					fmt.Print("\r\n")
					return "", io.EOF
				}
				if s.cursor < len(s.buffer) {
					copy(s.buffer[s.cursor:], s.buffer[s.cursor+1:])
					s.buffer = s.buffer[:len(s.buffer)-1]
				}

			case keyCtrlA:
				s.cursor = s.lineStart(s.cursor)

			case keyCtrlE:
				s.cursor = s.lineEnd(s.cursor)

			case keyCtrlK:
				end := s.lineEnd(s.cursor)
				if end > s.cursor {
					copy(s.buffer[s.cursor:], s.buffer[end:])
					s.buffer = s.buffer[:len(s.buffer)-(end-s.cursor)]
				}

			case keyCtrlU:
				start := s.lineStart(s.cursor)
				if start < s.cursor {
					copy(s.buffer[start:], s.buffer[s.cursor:])
					s.buffer = s.buffer[:len(s.buffer)-(s.cursor-start)]
					s.cursor = start
				}

			case keyCtrlW:
				s.deletePrevWord()

			case keyTab:
				s.insertRune('\t')

			// ── Escape sequences ───────────────────────────
			case keyEsc:
				rem := n - i - 1
				consumed := s.handleEscSeq(buf[i+1:], rem)
				i += consumed

			// ── Printable characters (including UTF-8 multi-byte) ─
			default:
				if b >= 0x20 {
					r, size := utf8.DecodeRune(buf[i:])
					if r != utf8.RuneError {
						s.insertRune(r)
						i += size - 1
					}
				}
			}

			s.render()
		}
	}
}

// ── buffer helpers ────────────────────────────────────────────────────

func (s *inputState) insertRune(r rune) {
	s.buffer = append(s.buffer, 0) // grow
	copy(s.buffer[s.cursor+1:], s.buffer[s.cursor:])
	s.buffer[s.cursor] = r
	s.cursor++
}

func (s *inputState) lineStart(pos int) int {
	for pos > 0 && s.buffer[pos-1] != '\n' {
		pos--
	}
	return pos
}

func (s *inputState) lineEnd(pos int) int {
	for pos < len(s.buffer) && s.buffer[pos] != '\n' {
		pos++
	}
	return pos
}

func (s *inputState) deletePrevWord() {
	// Skip trailing whitespace.
	for s.cursor > 0 && (s.buffer[s.cursor-1] == ' ' || s.buffer[s.cursor-1] == '\t') {
		s.cursor--
		copy(s.buffer[s.cursor:], s.buffer[s.cursor+1:])
		s.buffer = s.buffer[:len(s.buffer)-1]
	}
	// Delete word characters.
	for s.cursor > 0 {
		r := s.buffer[s.cursor-1]
		if r == ' ' || r == '\t' || r == '\n' {
			break
		}
		s.cursor--
		copy(s.buffer[s.cursor:], s.buffer[s.cursor+1:])
		s.buffer = s.buffer[:len(s.buffer)-1]
	}
}

// runeDisplayWidth returns the terminal display width of a rune.
// CJK and other wide characters occupy 2 columns; most others occupy 1.
func runeDisplayWidth(r rune) int {
	if r < 0x20 || r == 0x7F {
		return 0 // control characters
	}
	if r < 0x80 {
		return 1 // ASCII
	}
	// Halfwidth Katakana and halfwidth punctuation (narrow)
	if r >= 0xFF61 && r <= 0xFFDC || r >= 0xFFE8 && r <= 0xFFEE {
		return 1
	}
	// Fullwidth forms
	if r >= 0xFF01 && r <= 0xFF60 || r >= 0xFFE0 && r <= 0xFFE6 {
		return 2
	}
	// CJK punctuation and symbols
	if r >= 0x3000 && r <= 0x303F || r >= 0xFE30 && r <= 0xFE4F {
		return 2
	}
	// Unicode script-based wide characters
	if unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r) {
		return 2
	}
	return 1
}

// cursorPos returns (line, col) for a given buffer position using rune count.
// Used for buffer navigation (moveUp/moveDown).
func (s *inputState) cursorPos(pos int) (line, col int) {
	for i := 0; i < pos; i++ {
		if s.buffer[i] == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return
}

// displayPos returns (line, displayCol) for a given buffer position.
// displayCol accounts for CJK/fullwidth characters occupying 2 terminal columns.
func (s *inputState) displayPos(pos int) (line, col int) {
	for i := 0; i < pos; i++ {
		if s.buffer[i] == '\n' {
			line++
			col = 0
		} else {
			col += runeDisplayWidth(s.buffer[i])
		}
	}
	return
}

// displayCol translates a buffer column into a display column by adding the
// prompt or continuation prefix width.
func (s *inputState) displayCol(line, bufCol int) int {
	if line == 0 {
		return len(s.prompt) + bufCol
	}
	return len(s.cont) + bufCol
}

// ── escape sequence handling ──────────────────────────────────────────

// handleEscSeq parses a VT/xterm escape sequence from buf and returns how
// many bytes were consumed.
func (s *inputState) handleEscSeq(buf []byte, max int) int {
	if max < 1 {
		return 0
	}
	if buf[0] != '[' && buf[0] != 'O' {
		// Unrecognised; consume the one byte.
		return 1
	}
	seqType := buf[0]

	// Collect until a final byte (0x40–0x7E).
	end := 1
	for end < max && (buf[end] < 0x40 || buf[end] > 0x7E) {
		end++
	}
	if end >= max {
		return end // incomplete, consume what we have
	}
	final := buf[end]
	consumed := end + 1

	params := string(buf[1:end])

	switch {
	case seqType == '[' && final == 'A': // Up
		s.moveUp()
	case seqType == '[' && final == 'B': // Down
		s.moveDown()
	case seqType == '[' && final == 'C': // Right
		if s.cursor < len(s.buffer) {
			s.cursor++
		}
	case seqType == '[' && final == 'D': // Left
		if s.cursor > 0 {
			s.cursor--
		}
	case seqType == '[' && final == 'H': // Home
		s.cursor = s.lineStart(s.cursor)
	case seqType == '[' && final == 'F': // End
		s.cursor = s.lineEnd(s.cursor)
	case seqType == '[' && final == '~':
		switch params {
		case "3": // Delete forward
			if s.cursor < len(s.buffer) {
				copy(s.buffer[s.cursor:], s.buffer[s.cursor+1:])
				s.buffer = s.buffer[:len(s.buffer)-1]
			}
		case "1", "7": // Home (some terminals)
			s.cursor = s.lineStart(s.cursor)
		case "4", "8": // End (some terminals)
			s.cursor = s.lineEnd(s.cursor)
		}
	case seqType == 'O' && final == 'H': // Home (application mode)
		s.cursor = s.lineStart(s.cursor)
	case seqType == 'O' && final == 'F': // End (application mode)
		s.cursor = s.lineEnd(s.cursor)
	}

	return consumed
}

func (s *inputState) moveUp() {
	curLine, curCol := s.cursorPos(s.cursor)
	if curLine == 0 {
		s.cursor = 0
		return
	}
	// Find start of current line
	start := s.lineStart(s.cursor)
	// Find start of previous line
	prevStart := s.lineStart(start - 1)
	prevLen := start - prevStart - 1 // length of previous line (excluding \n)
	if curCol > prevLen {
		curCol = prevLen
	}
	s.cursor = prevStart + curCol
}

func (s *inputState) moveDown() {
	_, curCol := s.cursorPos(s.cursor)
	// Check if there is a next line
	end := s.lineEnd(s.cursor)
	if end >= len(s.buffer) {
		return // no next line
	}
	// Skip the \n
	nextStart := end + 1
	nextEnd := s.lineEnd(nextStart)
	nextLen := nextEnd - nextStart
	if curCol > nextLen {
		curCol = nextLen
	}
	s.cursor = nextStart + curCol
}

// ── rendering ──────────────────────────────────────────────────────────

// totalLines returns how many display lines the buffer occupies.
func (s *inputState) totalLines() int {
	if len(s.buffer) == 0 {
		return 1
	}
	lines := 1
	for _, r := range s.buffer {
		if r == '\n' {
			lines++
		}
	}
	return lines
}

// render clears the input area and draws the current buffer.
func (s *inputState) render() {
	var sb strings.Builder

	// Move cursor up to start of input area (relative to previous cursor position).
	if s.lastCurLine > 0 {
		sb.WriteString(fmt.Sprintf("\x1b[%dA", s.lastCurLine))
	}
	sb.WriteString("\r")
	sb.WriteString("\x1b[0J") // erase from cursor to end of screen

	// Draw buffer.
	sb.WriteString(s.prompt)
	line := 0
	for _, r := range s.buffer {
		if r == '\n' {
			sb.WriteString("\r\n")
			sb.WriteString(s.cont)
			line++
		} else {
			sb.WriteRune(r)
		}
	}

	// Compute where the cursor should be relative to the end of buffer.
	// Use displayPos so CJK/fullwidth chars are counted as 2 columns.
	curLine, curCol := s.displayPos(s.cursor)
	endLine, _ := s.displayPos(len(s.buffer))

	// Move from end-of-buffer position back to cursor position.
	if endLine > curLine {
		sb.WriteString(fmt.Sprintf("\x1b[%dA", endLine-curLine))
	} else if curLine > endLine {
		sb.WriteString(fmt.Sprintf("\x1b[%dB", curLine-endLine))
	}
	sb.WriteString("\r")
	dc := s.displayCol(curLine, curCol)
	if dc > 0 {
		sb.WriteString(fmt.Sprintf("\x1b[%dC", dc))
	}

	fmt.Print(sb.String())

	s.lastCurLine = curLine
}

package client

import (
	"bufio"
	"io"
	"strings"
)

// lineReader lê linhas de tamanho arbitrário (eventos podem ser grandes).
type lineReader struct{ br *bufio.Reader }

func newLineReader(r io.Reader) *lineReader { return &lineReader{br: bufio.NewReaderSize(r, 64*1024)} }

func (l *lineReader) next() (string, error) {
	var sb strings.Builder
	for {
		chunk, isPrefix, err := l.br.ReadLine()
		if err != nil {
			return "", err
		}
		sb.Write(chunk)
		if !isPrefix {
			return sb.String(), nil
		}
	}
}

package llm

import (
	"bufio"
	"bytes"
	"io"
)

// ReadSSE calls onData with the data payload of each server-sent event until
// EOF or a "[DONE]" sentinel. Multi-line data fields are joined with "\n".
func ReadSSE(r io.Reader, onData func(data []byte) error) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var data []byte
	have := false
	dispatch := func() error {
		if !have {
			return nil
		}
		d := data
		data, have = nil, false
		return onData(d)
	}
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimRight(line, "\r\n")
			switch {
			case len(line) == 0:
				if derr := dispatch(); derr != nil {
					return derr
				}
			case line[0] == ':':
			case bytes.HasPrefix(line, []byte("data:")):
				v := bytes.TrimPrefix(line[5:], []byte(" "))
				if bytes.Equal(v, []byte("[DONE]")) {
					return nil
				}
				if have {
					data = append(data, '\n')
				}
				data = append(data, v...)
				have = true
			}
		}
		if err == io.EOF {
			return dispatch()
		}
		if err != nil {
			return err
		}
	}
}

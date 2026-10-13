package files

import (
	"os"
	"regexp"
)

// SniffPageCount is a best-effort page-count estimate for a PDF: it reads the
// head (up to 2MB) and tail (up to 1MB) of the file, matches /Count <n> and
// returns the largest match below 200000. Any failure (missing file, read
// error, no match) degrades gracefully to 0 with a nil error, mirroring the
// reference implementation's promise-resolve(0) behavior.
func SniffPageCount(path string) (int, error) {
	data, err := readHeadTail(path)
	if err != nil {
		return 0, nil // best-effort: never surface errors
	}
	return maxCount(data), nil
}

const (
	sniffHead = 2 * 1024 * 1024 // bytes read from the start
	sniffTail = 1024 * 1024     // bytes read from the end
	sniffMax  = 200000          // counts >= this are rejected as bogus
)

var countRe = regexp.MustCompile(`/Count\s+(\d+)`)

func readHeadTail(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()

	head := min(size, sniffHead)
	tail := min(max(size-head, 0), sniffTail)

	buf := make([]byte, 0, head+tail)
	part := make([]byte, head)
	if _, err := f.ReadAt(part, 0); err != nil {
		return nil, err
	}
	buf = append(buf, part...)
	if tail > 0 {
		part = make([]byte, tail)
		if _, err := f.ReadAt(part, size-tail); err != nil {
			return nil, err
		}
		buf = append(buf, part...)
	}
	return buf, nil
}

func maxCount(data []byte) int {
	max := 0
	for _, m := range countRe.FindAllSubmatch(data, -1) {
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
			if n >= sniffMax { // clamp early; also guards against overflow
				n = sniffMax
				break
			}
		}
		if n > max && n < sniffMax {
			max = n
		}
	}
	return max
}

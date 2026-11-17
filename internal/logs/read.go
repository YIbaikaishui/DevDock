package logs

import (
	"bufio"
	"io"
	"strings"
)

// MaxLineBytes 是单行日志的硬上限。
// 超出部分被截断并追加标记，避免子进程输出超长行时把我们撑爆。
const MaxLineBytes = 64 * 1024

// truncatedMarker 追加在被截断的行尾。
const truncatedMarker = " [truncated]"

// ReadLines 持续从 r 读取行并回调 onLine。
//
// 实现要点：不用 bufio.Scanner —— 它遇到超过缓冲上限的行会直接返回错误
// 并停止读取，等于半路丢日志。这里用 bufio.Reader 手动处理超长行，
// 保证"永远继续读下去"。
func ReadLines(r io.Reader, onLine func(text string)) error {
	br := bufio.NewReaderSize(r, MaxLineBytes)

	for {
		line, err := readBoundedLine(br)
		if len(line) > 0 {
			onLine(line)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// readBoundedLine 读取一行，长度上限 MaxLineBytes。
// 超限时丢弃剩余部分直到换行，并追加截断标记。
func readBoundedLine(br *bufio.Reader) (string, error) {
	var sb strings.Builder
	total := 0
	truncated := false

	for {
		chunk, err := br.ReadSlice('\n')
		if len(chunk) > 0 {
			text := chunk
			if !truncated {
				remaining := MaxLineBytes - total
				if len(text) > remaining {
					text = text[:remaining]
					truncated = true
				}
				sb.Write(text)
				total += len(text)
			}
		}
		if err == bufio.ErrBufferFull {
			// 该行仍未结束，继续读，但不再累积。
			truncated = true
			continue
		}
		if err == nil || err == io.EOF {
			out := strings.TrimRight(sb.String(), "\r\n")
			if truncated {
				out += truncatedMarker
			}
			return out, err
		}
		return sb.String(), err
	}
}

package client

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
)

// StreamEvents 订阅某个项目的 SSE 日志流，返回日志文本通道与错误通道。
// 通道在流结束或出错时关闭。调用方通过关闭返回的 stop 函数停止订阅。
func (c *Client) StreamEvents(projectID string) (<-chan string, <-chan error) {
	out := make(chan string, 128)
	errCh := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errCh)

		url := c.BaseURL + "/api/projects/" + projectID + "/events?t=" + c.Token
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			errCh <- err
			return
		}
		// SSE 是长连接，不能用带超时的 client。
		hc := &http.Client{}
		resp, err := hc.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			errCh <- &APIError{Status: resp.StatusCode, Message: "无法订阅日志流"}
			return
		}

		sc := bufio.NewScanner(resp.Body)
		// 单条 SSE 消息可能较长，放宽缓冲上限。
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

		var eventName string
		var dataLines []string

		emit := func() {
			if eventName == "log" && len(dataLines) > 0 {
				payload := strings.Join(dataLines, "\n")
				var rec struct {
					Text string `json:"text"`
				}
				if json.Unmarshal([]byte(payload), &rec) == nil && rec.Text != "" {
					select {
					case out <- rec.Text:
					default:
						// 消费端太慢时丢弃，避免阻塞读取循环。
					}
				}
			}
			eventName = ""
			dataLines = dataLines[:0]
		}

		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				emit()
			case strings.HasPrefix(line, "event:"):
				eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if err := sc.Err(); err != nil {
			errCh <- err
		}
	}()

	return out, errCh
}

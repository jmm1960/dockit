package dockit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

type notification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}
type bark struct {
	endpoint, key, group string
	client               *http.Client
	queue                chan notification
}

func newBark(c config) (*bark, error) {
	u, err := url.Parse(c.barkServer)
	if err != nil || u.Host == "" || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("DOCKIT_BARK_SERVER must be an HTTPS URL without credentials, query or fragment")
	}
	return &bark{endpoint: strings.TrimRight(c.barkServer, "/") + "/push", key: c.barkKey, group: c.barkGroup,
		client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, queue: make(chan notification, 64)}, nil
}

func (b *bark) notify(title, body string) {
	if b.key == "" {
		return
	}
	select {
	case b.queue <- notification{title, body}:
	default:
		log.Warn("Bark queue full; notification dropped")
	}
}

func (b *bark) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case n := <-b.queue:
			for attempt := 0; attempt < 3; attempt++ {
				if err := b.send(ctx, n); err == nil {
					break
				} else if attempt == 2 {
					log.Warn("Bark delivery failed after 3 attempts")
				}
				if attempt < 2 {
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Duration(attempt+1) * time.Second):
					}
				}
			}
		}
	}
}

func (b *bark) send(ctx context.Context, n notification) error {
	payload, _ := json.Marshal(struct {
		notification
		Key   string `json:"device_key"`
		Group string `json:"group"`
	}{n, b.key, b.group})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("invalid Bark request")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("Bark transport failed")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Bark HTTP status %d", res.StatusCode)
	}
	var result struct {
		Code int `json:"code"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&result); err != nil {
		return fmt.Errorf("invalid Bark response")
	}
	if result.Code != 200 {
		return fmt.Errorf("Bark rejected notification")
	}
	return nil
}

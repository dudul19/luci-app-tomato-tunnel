package slowdns

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"www.bamsoftware.com/git/dnstt.git/turbotunnel"
)

const defaultRetryAfter = 10 * time.Second

type HTTPPacketConn struct {
	client *http.Client

	urlString string

	notBefore     time.Time
	notBeforeLock sync.RWMutex

	*turbotunnel.QueuePacketConn
}

func NewHTTPPacketConn(rt http.RoundTripper, urlString string, numSenders int) (*HTTPPacketConn, error) {
	c := &HTTPPacketConn{
		client: &http.Client{
			Transport: rt,
			Timeout:   1 * time.Minute,
		},
		urlString:       urlString,
		QueuePacketConn: turbotunnel.NewQueuePacketConn(turbotunnel.DummyAddr{}, 0),
	}
	for i := 0; i < numSenders; i++ {
		go c.sendLoop()
	}
	return c, nil
}

func (c *HTTPPacketConn) send(p []byte) error {
	req, err := http.NewRequest("POST", c.urlString, bytes.NewReader(p))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/dns-message")
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("User-Agent", "")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		if ct := resp.Header.Get("Content-Type"); ct != "application/dns-message" {
			return fmt.Errorf("unknown HTTP response Content-Type %+q", ct)
		}
		body, err := ioutil.ReadAll(io.LimitReader(resp.Body, 64000))
		if err == nil {
			c.QueuePacketConn.QueueIncoming(body, turbotunnel.DummyAddr{})
		}

	default:

		now := time.Now()
		var retryAfter time.Time
		if value := resp.Header.Get("Retry-After"); value != "" {
			var err error
			retryAfter, err = parseRetryAfter(value, now)
			if err != nil {
				log.Printf("cannot parse Retry-After value %+q", value)
			}
		}
		if retryAfter.IsZero() {

			retryAfter = now.Add(defaultRetryAfter)
		}
		if retryAfter.Before(now) {
			log.Printf("got %+q, but Retry-After is %v in the past",
				resp.Status, now.Sub(retryAfter))
		} else {
			c.notBeforeLock.Lock()
			if retryAfter.Before(c.notBefore) {
				log.Printf("got %+q, but Retry-After is %v earlier than already received Retry-After",
					resp.Status, c.notBefore.Sub(retryAfter))
			} else {
				log.Printf("got %+q; ceasing sending for %v",
					resp.Status, retryAfter.Sub(now))
				c.notBefore = retryAfter
			}
			c.notBeforeLock.Unlock()
		}
	}

	return nil
}

func (c *HTTPPacketConn) sendLoop() {
	for p := range c.QueuePacketConn.OutgoingQueue(turbotunnel.DummyAddr{}) {

		c.notBeforeLock.RLock()
		notBefore := c.notBefore
		c.notBeforeLock.RUnlock()
		if wait := notBefore.Sub(time.Now()); wait > 0 {

			continue
		}

		err := c.send(p)
		if err != nil {
			log.Printf("sendLoop: %v", err)
		}
	}
}

func parseRetryAfter(value string, now time.Time) (time.Time, error) {

	if t, err := http.ParseTime(value); err == nil {
		return t, nil
	}
	i, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return time.Time{}, err
	}
	return now.Add(time.Duration(i) * time.Second), nil
}

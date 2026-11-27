package slowdns

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"www.bamsoftware.com/git/dnstt.git/turbotunnel"
)

const dialTimeout = 30 * time.Second

type TLSPacketConn struct {
	*turbotunnel.QueuePacketConn
}

func NewTLSPacketConn(addr string, dialTLSContext func(ctx context.Context, network, addr string) (net.Conn, error)) (*TLSPacketConn, error) {
	dial := func() (net.Conn, error) {
		ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		defer cancel()
		return dialTLSContext(ctx, "tcp", addr)
	}

	conn, err := dial()
	if err != nil {
		return nil, err
	}
	c := &TLSPacketConn{
		QueuePacketConn: turbotunnel.NewQueuePacketConn(turbotunnel.DummyAddr{}, 0),
	}
	go func() {
		defer c.Close()
		for {
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				err := c.recvLoop(conn)
				if err != nil {
					log.Printf("recvLoop: %v", err)
				}
				wg.Done()
			}()
			go func() {
				err := c.sendLoop(conn)
				if err != nil {
					log.Printf("sendLoop: %v", err)
				}
				wg.Done()
			}()
			wg.Wait()
			conn.Close()

			conn, err = dial()
			if err != nil {
				log.Printf("dial tls: %v", err)
				break
			}
		}
	}()
	return c, nil
}

func (c *TLSPacketConn) recvLoop(conn net.Conn) error {
	br := bufio.NewReader(conn)
	for {
		var length uint16
		err := binary.Read(br, binary.BigEndian, &length)
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return err
		}
		p := make([]byte, int(length))
		_, err = io.ReadFull(br, p)
		if err != nil {
			return err
		}
		c.QueuePacketConn.QueueIncoming(p, turbotunnel.DummyAddr{})
	}
}

func (c *TLSPacketConn) sendLoop(conn net.Conn) error {
	bw := bufio.NewWriter(conn)
	for p := range c.QueuePacketConn.OutgoingQueue(turbotunnel.DummyAddr{}) {
		length := uint16(len(p))
		if int(length) != len(p) {
			panic(len(p))
		}
		err := binary.Write(bw, binary.BigEndian, &length)
		if err != nil {
			return err
		}
		_, err = bw.Write(p)
		if err != nil {
			return err
		}
		err = bw.Flush()
		if err != nil {
			return err
		}
	}
	return nil
}

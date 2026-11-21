package main

import (
	"crypto/rand"
	"crypto/rsa"
	"flag"
	"io"
	"log"
	"net"
	"strconv"

	"golang.org/x/crypto/ssh"
)

func main() {
	addr := flag.String("l", "127.0.0.1:2222", "listen")
	legacy := flag.Bool("legacy", true, "old algorithms only")
	flag.Parse()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	signer, _ := ssh.NewSignerFromKey(key)
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if c.User() == "dudul" && string(p) == "secret" {
				return nil, nil
			}
			return nil, io.EOF
		},
		ServerVersion: "SSH-2.0-dropbear_2013.60",
	}
	if *legacy {
		s, _ := ssh.NewSignerWithAlgorithms(signer.(ssh.AlgorithmSigner), []string{ssh.KeyAlgoRSA})
		cfg.AddHostKey(s)
		cfg.KeyExchanges = []string{ssh.InsecureKeyExchangeDH1SHA1}
		cfg.Ciphers = []string{ssh.InsecureCipherAES128CBC}
		cfg.MACs = []string{ssh.InsecureHMACSHA196}
	} else {
		cfg.AddHostKey(signer)
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("listening on %s", *addr)
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			_, chans, reqs, err := ssh.NewServerConn(c, cfg)
			if err != nil {
				log.Print(err)
				return
			}
			go ssh.DiscardRequests(reqs)
			for nc := range chans {
				var m struct {
					Host  string
					Port  uint32
					OHost string
					OPort uint32
				}
				ssh.Unmarshal(nc.ExtraData(), &m)
				up, err := net.Dial("tcp", net.JoinHostPort(m.Host, strconv.Itoa(int(m.Port))))
				if err != nil {
					nc.Reject(ssh.ConnectionFailed, err.Error())
					continue
				}
				ch, rq, _ := nc.Accept()
				go ssh.DiscardRequests(rq)
				go func() { io.Copy(ch, up); ch.CloseWrite() }()
				go func() { io.Copy(up, ch); up.Close() }()
			}
		}()
	}
}

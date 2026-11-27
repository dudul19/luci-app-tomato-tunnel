package slowdns

import (
	"bytes"
	"encoding/base32"
	"errors"
	"strings"

	"www.bamsoftware.com/git/dnstt.git/dns"
)

const (
	RRTypeA     uint16 = 1
	RRTypeAAAA  uint16 = 28
	RRTypeNS    uint16 = 2
	RRTypeCNAME uint16 = 5
	RRTypeNULL  uint16 = 10
	RRTypeMX    uint16 = 15
	RRTypeTXT   uint16 = 16
	RRTypeSRV   uint16 = 33

	RRTypeDNSKEY uint16 = 48
	RRTypeSVCB   uint16 = 64
	RRTypeHTTPS  uint16 = 65
)

var TypeNames = map[string]uint16{
	"txt": RRTypeTXT, "null": RRTypeNULL, "cname": RRTypeCNAME,
	"ns": RRTypeNS, "mx": RRTypeMX, "srv": RRTypeSRV,
	"https": RRTypeHTTPS, "svcb": RRTypeSVCB, "dnskey": RRTypeDNSKEY,
	"a": RRTypeA, "aaaa": RRTypeAAAA,
}

func isAddrType(t uint16) bool { return t == RRTypeA || t == RRTypeAAAA }

var dns64WKP = [12]byte{0x00, 0x64, 0xff, 0x9b}

func unDNS64(datas [][]byte) ([][]byte, bool) {
	if len(datas) == 0 {
		return nil, false
	}
	var pre [12]byte
	for i, d := range datas {
		if len(d) != 16 {
			return nil, false
		}
		var p [12]byte
		copy(p[:], d[:12])
		if i == 0 {
			pre = p
		} else if p != pre {
			return nil, false
		}
	}
	if pre != dns64WKP && len(datas) < 2 {
		return nil, false
	}
	out := make([][]byte, len(datas))
	for i, d := range datas {
		out[i] = d[12:16]
	}
	return out, true
}

func IsDNS64(datas [][]byte) bool {
	_, ok := unDNS64(datas)
	return ok
}

func decodeAddrAnswers(t uint16, datas [][]byte) ([]byte, error) {
	if t == RRTypeAAAA {
		if a, ok := unDNS64(datas); ok {
			datas, t = a, RRTypeA
		}
	}
	width := 4
	if t == RRTypeAAAA {
		width = 16
	}
	if len(datas) == 0 || len(datas) > 256 {
		return nil, errRData
	}
	chunks := make([][]byte, len(datas))
	for _, d := range datas {
		if len(d) != width {
			return nil, errRData
		}
		i := int(d[0])
		if i >= len(chunks) || chunks[i] != nil {
			return nil, errRData
		}
		chunks[i] = d[1:]
	}
	framed := make([]byte, 0, len(datas)*(width-1))
	for _, c := range chunks {
		framed = append(framed, c...)
	}
	if len(framed) < 2 {
		return nil, errRData
	}
	n := int(framed[0])<<8 | int(framed[1])
	if 2+n > len(framed) {
		return nil, errRData
	}
	return framed[2 : 2+n], nil
}

func TypeName(t uint16) string {
	for k, v := range TypeNames {
		if v == t {
			return strings.ToUpper(k)
		}
	}
	return "?"
}

var nameBase32 = base32.StdEncoding.WithPadding(base32.NoPadding)

var errRData = errors.New("bad rdata")

func decodeRData(t uint16, p []byte, domain dns.Name) ([]byte, error) {
	switch t {
	case RRTypeTXT:
		return dns.DecodeRDataTXT(p)
	case RRTypeNULL:
		return p, nil
	case RRTypeCNAME, RRTypeNS:
		return decodeName(p, domain)
	case RRTypeMX:
		if len(p) < 2 {
			return nil, errRData
		}
		return decodeName(p[2:], domain)
	case RRTypeSRV:
		if len(p) < 6 {
			return nil, errRData
		}
		return decodeName(p[6:], domain)
	case RRTypeHTTPS, RRTypeSVCB:
		return decodeSvcb(p)
	case RRTypeDNSKEY:
		return decodeDnskey(p)
	}
	return nil, errRData
}

func skipName(p []byte) (int, error) {
	for i := 0; i < len(p); {
		n := int(p[i])
		if n == 0 {
			return i + 1, nil
		}
		if n&0xc0 == 0xc0 {
			if i+2 > len(p) {
				return 0, errRData
			}
			return i + 2, nil
		}
		i += 1 + n
	}
	return 0, errRData
}

func decodeSvcb(p []byte) ([]byte, error) {
	if len(p) < 3 {
		return nil, errRData
	}
	n, err := skipName(p[2:])
	if err != nil {
		return nil, err
	}
	for q := p[2+n:]; len(q) >= 4; {
		key, l := int(q[0])<<8|int(q[1]), int(q[2])<<8|int(q[3])
		if 4+l > len(q) {
			return nil, errRData
		}
		if v := q[4 : 4+l]; key == 0xff00 && len(v) >= 1 && v[0] == 0x01 {
			return v[1:], nil
		}
		q = q[4+l:]
	}
	return nil, errRData
}

func decodeDnskey(p []byte) ([]byte, error) {
	if len(p) < 5 {
		return nil, errRData
	}
	n, err := skipName(p[4:])
	if err != nil {
		return nil, err
	}
	return p[4+n:], nil
}

var nameSuffix = dns.Name{[]byte("dnstt"), []byte("invalid")}

func decodeName(p []byte, domain dns.Name) ([]byte, error) {
	var labels [][]byte
	for i := 0; i < len(p); {
		n := int(p[i])
		if n == 0 || n&0xc0 == 0xc0 {
			break
		}
		if i+1+n > len(p) {
			return nil, errRData
		}
		labels = append(labels, p[i+1:i+1+n])
		i += 1 + n
	}
	if len(labels) >= len(nameSuffix) && sameLabels(labels[len(labels)-len(nameSuffix):], nameSuffix) {
		labels = labels[:len(labels)-len(nameSuffix)]
	} else {
		for k := len(domain); k > 0; k-- {
			if len(labels) >= k && sameLabels(labels[len(labels)-k:], domain[:k]) {
				labels = labels[:len(labels)-k]
				break
			}
		}
	}
	enc := bytes.ToUpper(bytes.Join(labels, nil))
	out := make([]byte, nameBase32.DecodedLen(len(enc)))
	n, err := nameBase32.Decode(out, enc)
	if err != nil {
		return nil, err
	}
	return out[:n], nil
}

func sameLabels(a [][]byte, b dns.Name) bool {
	for i := range a {
		if !bytes.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

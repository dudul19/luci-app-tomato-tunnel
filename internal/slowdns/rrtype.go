package slowdns

import (
	"bytes"
	"encoding/base32"
	"errors"
	"strings"

	"www.bamsoftware.com/git/dnstt.git/dns"
)

const (
	RRTypeNS    uint16 = 2
	RRTypeCNAME uint16 = 5
	RRTypeNULL  uint16 = 10
	RRTypeMX    uint16 = 15
	RRTypeTXT   uint16 = 16
	RRTypeSRV   uint16 = 33
)

var TypeNames = map[string]uint16{
	"txt": RRTypeTXT, "null": RRTypeNULL, "cname": RRTypeCNAME,
	"ns": RRTypeNS, "mx": RRTypeMX, "srv": RRTypeSRV,
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
	}
	return nil, errRData
}

// decodeName reads base32 labels up to the end of the name or a compression
// pointer, drops the tunnel domain suffix and decodes the rest.
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
	for k := len(domain); k > 0; k-- {
		if len(labels) >= k && sameLabels(labels[len(labels)-k:], domain[:k]) {
			labels = labels[:len(labels)-k]
			break
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

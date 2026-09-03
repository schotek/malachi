package compose

import (
	"errors"
	"net/url"
	"strings"
)

// ParseMailto turns a mailto: URI into compose parameters. Only the
// standard fields are honoured (to, cc, bcc, subject, body); the body is
// escaped for the editor. This is the "split the query string" the desktop
// file promises: no interpretation beyond that.
func ParseMailto(uri string) (Params, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return Params{}, err
	}
	if !strings.EqualFold(u.Scheme, "mailto") {
		return Params{}, errors.New("not a mailto: URI")
	}
	var p Params
	if to, err := url.PathUnescape(u.Opaque); err == nil {
		p.To, _ = ParseAddressList(to)
	}
	for key, values := range u.Query() {
		if len(values) == 0 {
			continue
		}
		v := values[0]
		switch strings.ToLower(key) {
		case "to":
			more, _ := ParseAddressList(v)
			p.To = append(p.To, more...)
		case "cc":
			p.CC, _ = ParseAddressList(v)
		case "bcc":
			p.BCC, _ = ParseAddressList(v)
		case "subject":
			p.Subject = strings.TrimSpace(v)
		case "body":
			p.BodyHTML = escapeText(v)
		}
	}
	return p, nil
}

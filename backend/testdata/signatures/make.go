// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

//go:build ignore

// make.go writes the signature test data (docs/plans/signatures.md §7): a
// test CA with an intermediate, the certificates and keys of the signers,
// OpenPGP keys, and the signed messages of backend/testdata/mime
// (smime-*.eml, pgp-*.eml). Everything here is for tests only: the CA is
// never in a production pool, the keys sign nothing but these fixtures.
//
// Run from backend/:
//
//	go run ./testdata/signatures/make.go
//
// Every run makes new keys, so every fixture changes; commit them
// together. The validity of the certificates is fixed (2026–2046, a short
// one 2024–2025) so the fixtures do not expire with the calendar; tests
// pass the arrival time.
package main

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	_ "crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	_ "crypto/sha1"
	_ "crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"golang.org/x/crypto/cryptobyte"
	cbasn1 "golang.org/x/crypto/cryptobyte/asn1"
)

const (
	sigDir  = "testdata/signatures"
	mimeDir = "testdata/mime"
	// OCSPURL and CRLURL are what the leaf certificates name; tests route
	// these hosts to internal/revocation/revocationtest.
	ocspURL = "http://ocsp.malachi-test.example/ocsp"
	crlURL  = "http://crl.malachi-test.example/crl"
)

var (
	start = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end   = time.Date(2046, 1, 1, 0, 0, 0, 0, time.UTC)
	// caStart lets the CAs cover the short certificate of 2024.
	caStart       = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	date          = "Thu, 01 Oct 2026 10:00:00 +0000"
	serialN int64 = 1000
)

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func serial() *big.Int { serialN++; return big.NewInt(serialN) }

func rsaKey(bits int) *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, bits)
	check(err)
	return k
}

type pair struct {
	cert *x509.Certificate
	key  crypto.Signer
}

func ca(name string, parent *pair) *pair {
	key := rsaKey(2048)
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: name, Organization: []string{"Malachi Test"}},
		NotBefore:             caStart,
		NotAfter:              end,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	signer, parentCert := crypto.Signer(key), tmpl
	if parent != nil {
		signer, parentCert = parent.key, parent.cert
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parentCert, key.Public(), signer)
	check(err)
	c, err := x509.ParseCertificate(der)
	check(err)
	return &pair{c, key}
}

type leafOpts struct {
	cn         string
	emails     []string
	dnEmail    string
	key        crypto.Signer
	ekus       []x509.ExtKeyUsage
	notBefore  time.Time
	notAfter   time.Time
	selfSigned bool
	ocsp       bool
}

func leaf(issuer *pair, o leafOpts) *pair {
	if o.key == nil {
		o.key = rsaKey(2048)
	}
	if o.ekus == nil {
		o.ekus = []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection}
	}
	if o.notBefore.IsZero() {
		o.notBefore, o.notAfter = start, end
	}
	subj := pkix.Name{CommonName: o.cn, Organization: []string{"Malachi Test"}}
	if o.dnEmail != "" {
		subj.ExtraNames = []pkix.AttributeTypeAndValue{{Type: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 1}, Value: o.dnEmail}}
	}
	tmpl := &x509.Certificate{
		SerialNumber:   serial(),
		Subject:        subj,
		NotBefore:      o.notBefore,
		NotAfter:       o.notAfter,
		KeyUsage:       x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:    o.ekus,
		EmailAddresses: o.emails,
	}
	if o.ocsp {
		tmpl.OCSPServer = []string{ocspURL}
		tmpl.CRLDistributionPoints = []string{crlURL}
	}
	var parent *x509.Certificate
	var signer crypto.Signer
	if o.selfSigned {
		parent, signer = tmpl, o.key
	} else {
		parent, signer = issuer.cert, issuer.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, o.key.Public(), signer)
	check(err)
	c, err := x509.ParseCertificate(der)
	check(err)
	return &pair{c, o.key}
}

func writePEM(name, typ string, der []byte) {
	var b bytes.Buffer
	check(pem.Encode(&b, &pem.Block{Type: typ, Bytes: der}))
	check(os.WriteFile(filepath.Join(sigDir, name), b.Bytes(), 0o644))
}

func writeKey(name string, k crypto.Signer) {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	check(err)
	writePEM(name, "PRIVATE KEY", der)
}

// --- CMS -------------------------------------------------------------------

var (
	oidData          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidSignedData    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidContentType   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidMessageDigest = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidSigningTime   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 5}
	oidSHA256        = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSHA1          = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidMD5           = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 5}
	oidRSA           = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidECDSA256      = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
	oidDSASHA256     = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 3, 2}
)

type cmsOpts struct {
	attached   bool
	hash       crypto.Hash
	digestOID  asn1.ObjectIdentifier
	sigOID     asn1.ObjectIdentifier
	certs      []*x509.Certificate
	signers    []*pair // signer infos (the first is the real one)
	signWith   crypto.Signer
	junkSig    bool
	extraCerts int // filler certificates to add (the long chain)
}

func sha(h crypto.Hash, b []byte) []byte {
	d := h.New()
	d.Write(b)
	return d.Sum(nil)
}

func addAlg(b *cryptobyte.Builder, oid asn1.ObjectIdentifier, null bool) {
	b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) {
		b.AddASN1ObjectIdentifier(oid)
		if null {
			b.AddASN1NULL()
		}
	})
}

func signerInfo(b *cryptobyte.Builder, p *pair, content []byte, o cmsOpts) {
	// Signed attributes as a SET (what is signed), then tagged [0].
	attrs := cryptobyte.NewBuilder(nil)
	attrs.AddASN1(cbasn1.SET, func(b *cryptobyte.Builder) {
		b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) {
			b.AddASN1ObjectIdentifier(oidContentType)
			b.AddASN1(cbasn1.SET, func(b *cryptobyte.Builder) { b.AddASN1ObjectIdentifier(oidData) })
		})
		b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) {
			b.AddASN1ObjectIdentifier(oidSigningTime)
			b.AddASN1(cbasn1.SET, func(b *cryptobyte.Builder) { b.AddASN1UTCTime(time.Date(2026, 10, 1, 9, 59, 0, 0, time.UTC)) })
		})
		b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) {
			b.AddASN1ObjectIdentifier(oidMessageDigest)
			b.AddASN1(cbasn1.SET, func(b *cryptobyte.Builder) { b.AddASN1OctetString(sha(o.hash, content)) })
		})
	})
	set := attrs.BytesOrPanic()
	signer := p.key
	if o.signWith != nil {
		signer = o.signWith
	}
	var sig []byte
	var err error
	digest := sha(o.hash, set)
	switch k := signer.(type) {
	case *ecdsa.PrivateKey:
		sig, err = ecdsa.SignASN1(rand.Reader, k, digest)
	default:
		sig, err = signer.Sign(rand.Reader, digest, o.hash)
	}
	check(err)
	if o.junkSig {
		sig = []byte("not a signature at all")
	}
	tagged := append([]byte{}, set...)
	tagged[0] = 0xa0
	b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) {
		b.AddASN1Int64(1)
		b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) {
			b.AddBytes(p.cert.RawIssuer)
			b.AddASN1BigInt(p.cert.SerialNumber)
		})
		addAlg(b, o.digestOID, true)
		b.AddBytes(tagged)
		addAlg(b, o.sigOID, o.sigOID.Equal(oidRSA))
		b.AddASN1OctetString(sig)
	})
}

func cms(content []byte, o cmsOpts) []byte {
	if o.hash == 0 {
		o.hash = crypto.SHA256
	}
	if o.digestOID == nil {
		o.digestOID = oidSHA256
	}
	if o.sigOID == nil {
		o.sigOID = oidRSA
	}
	b := cryptobyte.NewBuilder(nil)
	b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) {
		b.AddASN1ObjectIdentifier(oidSignedData)
		b.AddASN1(cbasn1.Tag(0).ContextSpecific().Constructed(), func(b *cryptobyte.Builder) {
			b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) {
				b.AddASN1Int64(1)
				b.AddASN1(cbasn1.SET, func(b *cryptobyte.Builder) { addAlg(b, o.digestOID, true) })
				b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) {
					b.AddASN1ObjectIdentifier(oidData)
					if o.attached {
						b.AddASN1(cbasn1.Tag(0).ContextSpecific().Constructed(), func(b *cryptobyte.Builder) {
							b.AddASN1OctetString(content)
						})
					}
				})
				b.AddASN1(cbasn1.Tag(0).ContextSpecific().Constructed(), func(b *cryptobyte.Builder) {
					for _, c := range o.certs {
						b.AddBytes(c.Raw)
					}
					for i := 0; i < o.extraCerts; i++ {
						b.AddBytes(o.certs[0].Raw)
					}
				})
				b.AddASN1(cbasn1.SET, func(b *cryptobyte.Builder) {
					for _, s := range o.signers {
						signerInfo(b, s, content, o)
					}
				})
			})
		})
	})
	return b.BytesOrPanic()
}

// --- messages --------------------------------------------------------------

func b64(data []byte) string {
	s := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	for len(s) > 76 {
		b.WriteString(s[:76] + "\r\n")
		s = s[76:]
	}
	b.WriteString(s + "\r\n")
	return b.String()
}

func headers(from, subject, id string, extra ...string) string {
	h := "From: " + from + "\r\nTo: Bob <bob@example.net>\r\nSubject: " + subject + "\r\nDate: " + date +
		"\r\nMessage-ID: <" + id + "@example.org>\r\nMIME-Version: 1.0\r\n"
	for _, e := range extra {
		h += e + "\r\n"
	}
	return h
}

const textEntity = "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 7bit\r\n\r\n" +
	"Hello Bob,\r\n\r\nthe report is attached to this signed message.\r\n\r\nAlice\r\n"

func clearSigned(hdr, signed string, sig []byte) string {
	return hdr + "Content-Type: multipart/signed; protocol=\"application/pkcs7-signature\"; micalg=sha-256; boundary=\"sig-b1\"\r\n\r\n" +
		"This is an S/MIME signed message.\r\n\r\n--sig-b1\r\n" + signed + "\r\n--sig-b1\r\n" +
		"Content-Type: application/pkcs7-signature; name=\"smime.p7s\"\r\nContent-Transfer-Encoding: base64\r\n" +
		"Content-Disposition: attachment; filename=\"smime.p7s\"\r\n\r\n" + b64(sig) + "\r\n--sig-b1--\r\n"
}

func opaque(hdr string, der []byte) string {
	return hdr + "Content-Type: application/pkcs7-mime; smime-type=signed-data; name=\"smime.p7m\"\r\n" +
		"Content-Transfer-Encoding: base64\r\nContent-Disposition: attachment; filename=\"smime.p7m\"\r\n\r\n" + b64(der)
}

func write(name, content string) {
	check(os.WriteFile(filepath.Join(mimeDir, name), []byte(content), 0o644))
}

func signClear(p *pair, signed string, o cmsOpts) []byte {
	o.signers = []*pair{p}
	if o.certs == nil {
		o.certs = []*x509.Certificate{p.cert}
	}
	return cms([]byte(signed), o)
}

// --- OpenPGP ---------------------------------------------------------------

func pgpEntity(name, email string) *openpgp.Entity {
	cfg := &packet.Config{Algorithm: packet.PubKeyAlgoEd25519, Time: func() time.Time { return start }}
	e, err := openpgp.NewEntity(name, "", email, cfg)
	check(err)
	return e
}

func pgpPublic(e *openpgp.Entity) string {
	var b bytes.Buffer
	w, err := armor.Encode(&b, openpgp.PublicKeyType, nil)
	check(err)
	check(e.Serialize(w))
	check(w.Close())
	return b.String()
}

func pgpPublicBinary(e *openpgp.Entity) []byte {
	var b bytes.Buffer
	check(e.Serialize(&b))
	return b.Bytes()
}

func pgpSign(e *openpgp.Entity, signed string) string {
	var b bytes.Buffer
	cfg := &packet.Config{Time: func() time.Time { return time.Date(2026, 10, 1, 9, 59, 0, 0, time.UTC) }}
	check(openpgp.ArmoredDetachSignText(&b, e, strings.NewReader(signed), cfg))
	return b.String()
}

func pgpSigned(hdr, signed, sig string) string {
	return hdr + "Content-Type: multipart/signed; micalg=pgp-sha256; protocol=\"application/pgp-signature\"; boundary=\"pgp-b1\"\r\n\r\n" +
		"This is an OpenPGP/MIME signed message (RFC 4880 and 3156)\r\n--pgp-b1\r\n" + signed + "\r\n--pgp-b1\r\n" +
		"Content-Type: application/pgp-signature; name=\"signature.asc\"\r\nContent-Description: OpenPGP digital signature\r\n" +
		"Content-Disposition: attachment; filename=\"signature.asc\"\r\n\r\n" + strings.ReplaceAll(sig, "\n", "\r\n") + "\r\n--pgp-b1--\r\n"
}

func autocrypt(addr string, e *openpgp.Entity) string {
	data := base64.StdEncoding.EncodeToString(pgpPublicBinary(e))
	var b strings.Builder
	b.WriteString("Autocrypt: addr=" + addr + "; prefer-encrypt=mutual; keydata=")
	for len(data) > 70 {
		b.WriteString("\r\n " + data[:70])
		data = data[70:]
	}
	b.WriteString("\r\n " + data)
	return b.String()
}

func main() {
	check(os.MkdirAll(sigDir, 0o755))

	root := ca("Malachi Test Root CA", nil)
	inter := ca("Malachi Test Intermediate CA", root)
	writePEM("root.pem", "CERTIFICATE", root.cert.Raw)
	writeKey("root.key", root.key)
	writePEM("intermediate.pem", "CERTIFICATE", inter.cert.Raw)
	writeKey("intermediate.key", inter.key)

	alice := leaf(inter, leafOpts{cn: "Alice Example", emails: []string{"alice@example.org"}, ocsp: true})
	writePEM("alice.pem", "CERTIFICATE", alice.cert.Raw)
	writeKey("alice.key", alice.key)
	// The OCSP responder of the revocation tests that is not meant for
	// OCSP signing (a plain e-mail certificate of the same CA).
	mallory := leaf(inter, leafOpts{cn: "Mallory Example", emails: []string{"mallory@example.org"}})
	writePEM("mallory.pem", "CERTIFICATE", mallory.cert.Raw)
	writeKey("mallory.key", mallory.key)

	chain := []*x509.Certificate{alice.cert, inter.cert}
	from := "Alice Example <alice@example.org>"

	// Clear signed, valid.
	write("smime-valid-clear.eml", clearSigned(headers(from, "Signed report", "smime-valid-clear"), textEntity,
		signClear(alice, textEntity, cmsOpts{certs: chain})))

	// The same with one byte of the body changed after signing.
	tampered := strings.Replace(textEntity, "report", "rep0rt", 1)
	write("smime-tampered.eml", clearSigned(headers(from, "Signed report", "smime-tampered"), tampered,
		signClear(alice, textEntity, cmsOpts{certs: chain})))

	// Stored with LF line endings (a client that saved it so).
	lf := clearSigned(headers(from, "Signed report", "smime-lf"), textEntity, signClear(alice, textEntity, cmsOpts{certs: chain}))
	write("smime-lf.eml", strings.ReplaceAll(lf, "\r\n", "\n"))

	// The signer info names Alice's certificate, another key signed.
	write("smime-other-cert.eml", clearSigned(headers(from, "Signed report", "smime-other-cert"), textEntity,
		signClear(alice, textEntity, cmsOpts{certs: chain, signWith: mallory.key})))

	// From is not the certificate's address.
	write("smime-from-mismatch.eml", clearSigned(headers("Bank <security@bank.example>", "Signed report", "smime-from-mismatch"), textEntity,
		signClear(alice, textEntity, cmsOpts{certs: chain})))

	// Two signers.
	write("smime-multiple-signers.eml", clearSigned(headers(from, "Signed report", "smime-multiple-signers"), textEntity,
		cms([]byte(textEntity), cmsOpts{certs: append(chain, mallory.cert), signers: []*pair{alice, mallory}})))

	// A signed message inside a signed message: the outer one is the
	// verdict.
	innerSigned := "Content-Type: multipart/signed; protocol=\"application/pkcs7-signature\"; micalg=sha-256; boundary=\"inner-b\"\r\n\r\n" +
		"--inner-b\r\n" + textEntity + "\r\n--inner-b\r\nContent-Type: application/pkcs7-signature; name=\"smime.p7s\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + b64(signClear(mallory, textEntity, cmsOpts{})) + "\r\n--inner-b--\r\n"
	write("smime-nested-signed.eml", clearSigned(headers(from, "Signed twice", "smime-nested-signed"), innerSigned,
		signClear(alice, innerSigned, cmsOpts{certs: chain})))

	// A signed message forwarded as an attachment: no verdict.
	signedMsg := clearSigned(headers(from, "Signed report", "smime-forwarded-inner"), textEntity, signClear(alice, textEntity, cmsOpts{certs: chain}))
	write("smime-forwarded.eml", headers("Eve <eve@example.com>", "Fwd: Signed report", "smime-forwarded")+
		"Content-Type: multipart/mixed; boundary=\"fwd-b\"\r\n\r\n--fwd-b\r\nContent-Type: text/plain\r\n\r\nSee below.\r\n"+
		"--fwd-b\r\nContent-Type: message/rfc822\r\nContent-Disposition: attachment; filename=\"report.eml\"\r\n\r\n"+signedMsg+"\r\n--fwd-b--\r\n")

	// The signed part, and an unsigned HTML part beside it: no verdict.
	signedPart := "Content-Type: multipart/signed; protocol=\"application/pkcs7-signature\"; micalg=sha-256; boundary=\"sig-b1\"\r\n\r\n" +
		"--sig-b1\r\n" + textEntity + "\r\n--sig-b1\r\nContent-Type: application/pkcs7-signature; name=\"smime.p7s\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + b64(signClear(alice, textEntity, cmsOpts{certs: chain})) + "\r\n--sig-b1--\r\n"
	write("smime-signed-plus-html.eml", headers(from, "Signed report", "smime-signed-plus-html")+
		"Content-Type: multipart/mixed; boundary=\"mix-b\"\r\n\r\n--mix-b\r\n"+signedPart+"\r\n--mix-b\r\n"+
		"Content-Type: text/html; charset=utf-8\r\n\r\n<p>You are fired.</p>\r\n--mix-b--\r\n")

	// A list's footer after the signed part (Q7).
	write("smime-footer.eml", headers(from, "[list] Signed report", "smime-footer", "List-Id: <test.lists.example.org>")+
		"Content-Type: multipart/mixed; boundary=\"mix-b\"\r\n\r\n--mix-b\r\n"+signedPart+"\r\n--mix-b\r\n"+
		"Content-Type: text/plain; charset=utf-8\r\n\r\n_______________________________________________\r\ntest mailing list\r\n--mix-b--\r\n")

	// A certificate valid 2024–2025: before or after the arrival.
	short := leaf(inter, leafOpts{cn: "Alice Example", emails: []string{"alice@example.org"},
		notBefore: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), notAfter: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)})
	write("smime-short-cert.eml", clearSigned(headers(from, "Signed report", "smime-short-cert"), textEntity,
		signClear(short, textEntity, cmsOpts{certs: []*x509.Certificate{short.cert, inter.cert}})))

	// SHA-1 and MD5 digests.
	write("smime-sha1.eml", clearSigned(headers(from, "Signed report", "smime-sha1"), textEntity,
		signClear(alice, textEntity, cmsOpts{certs: chain, hash: crypto.SHA1, digestOID: oidSHA1})))
	write("smime-md5.eml", clearSigned(headers(from, "Signed report", "smime-md5"), textEntity,
		signClear(alice, textEntity, cmsOpts{certs: chain, hash: crypto.MD5, digestOID: oidMD5})))

	// RSA 1024.
	small := leaf(inter, leafOpts{cn: "Alice Example", emails: []string{"alice@example.org"}, key: rsaKey(1024)})
	write("smime-rsa1024.eml", clearSigned(headers(from, "Signed report", "smime-rsa1024"), textEntity,
		signClear(small, textEntity, cmsOpts{certs: []*x509.Certificate{small.cert, inter.cert}})))

	// ECDSA P-256.
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	check(err)
	ecLeaf := leaf(inter, leafOpts{cn: "Alice Example", emails: []string{"alice@example.org"}, key: ec})
	write("smime-ecdsa.eml", clearSigned(headers(from, "Signed report", "smime-ecdsa"), textEntity,
		signClear(ecLeaf, textEntity, cmsOpts{certs: []*x509.Certificate{ecLeaf.cert, inter.cert}, sigOID: oidECDSA256})))

	// DSA named as the algorithm (the signature is junk: never checked).
	write("smime-dsa.eml", clearSigned(headers(from, "Signed report", "smime-dsa"), textEntity,
		signClear(alice, textEntity, cmsOpts{certs: chain, sigOID: oidDSASHA256, junkSig: true})))

	// A certificate for TLS servers only.
	server := leaf(inter, leafOpts{cn: "Alice Example", emails: []string{"alice@example.org"}, ekus: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	write("smime-no-email-protection.eml", clearSigned(headers(from, "Signed report", "smime-no-email-protection"), textEntity,
		signClear(server, textEntity, cmsOpts{certs: []*x509.Certificate{server.cert, inter.cert}})))

	// Twenty certificates.
	write("smime-chain-20.eml", clearSigned(headers(from, "Signed report", "smime-chain-20"), textEntity,
		signClear(alice, textEntity, cmsOpts{certs: chain, extraCerts: 18})))

	// Self-signed, and issued by a CA nobody trusts.
	self := leaf(nil, leafOpts{cn: "Alice Example", emails: []string{"alice@example.org"}, selfSigned: true})
	write("smime-self-signed.eml", clearSigned(headers(from, "Signed report", "smime-self-signed"), textEntity,
		signClear(self, textEntity, cmsOpts{certs: []*x509.Certificate{self.cert}})))
	rogue := ca("Rogue CA", nil)
	rogueLeaf := leaf(rogue, leafOpts{cn: "Alice Example", emails: []string{"alice@example.org"}})
	write("smime-unknown-ca.eml", clearSigned(headers(from, "Signed report", "smime-unknown-ca"), textEntity,
		signClear(rogueLeaf, textEntity, cmsOpts{certs: []*x509.Certificate{rogueLeaf.cert, rogue.cert}})))

	// Bidi and control characters in the name, the address only in the
	// DN (an old certificate).
	bidi := leaf(inter, leafOpts{cn: "Alice ‮elpmaxE‬\u0007 Admin", dnEmail: "alice@example.org"})
	write("smime-bidi-cn.eml", clearSigned(headers(from, "Signed report", "smime-bidi-cn"), textEntity,
		signClear(bidi, textEntity, cmsOpts{certs: []*x509.Certificate{bidi.cert, inter.cert}})))

	// Protected headers that differ from the outer ones.
	protected := "Content-Type: text/plain; charset=utf-8; protected-headers=\"v1\"\r\nSubject: The real subject\r\n" +
		"From: Alice Example <alice@example.org>\r\nTo: Bob <bob@example.net>\r\nDate: " + date + "\r\n\r\nHello Bob.\r\n"
	write("smime-protected-headers.eml", clearSigned(headers(from, "A different subject", "smime-protected-headers"), protected,
		signClear(alice, protected, cmsOpts{certs: chain})))

	// Opaque signed data with HTML and a cid: image inside.
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R'}
	inner := "Content-Type: multipart/related; boundary=\"rel-b\"\r\n\r\n--rel-b\r\n" +
		"Content-Type: multipart/alternative; boundary=\"alt-b\"\r\n\r\n--alt-b\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
		"Opaque hello from Alice.\r\n--alt-b\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" +
		"<p>Opaque <b>hello</b> from Alice.</p><img src=\"cid:logo@example.org\">\r\n--alt-b--\r\n" +
		"--rel-b\r\nContent-Type: image/png\r\nContent-ID: <logo@example.org>\r\nContent-Transfer-Encoding: base64\r\n\r\n" +
		b64(png) + "--rel-b\r\nContent-Type: application/pdf; name=\"report.pdf\"\r\nContent-Disposition: attachment; filename=\"report.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + b64([]byte("%PDF-1.4 test")) + "--rel-b--\r\n"
	opaqueDER := cms([]byte(inner), cmsOpts{attached: true, certs: chain, signers: []*pair{alice}})
	write("smime-valid-opaque.eml", opaque(headers(from, "Opaque signed report", "smime-valid-opaque"), opaqueDER))

	// Opaque inside opaque.
	innerOpaque := "Content-Type: application/pkcs7-mime; smime-type=signed-data; name=\"smime.p7m\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + b64(cms([]byte(textEntity), cmsOpts{attached: true, certs: chain, signers: []*pair{alice}}))
	write("smime-opaque-in-opaque.eml", opaque(headers(from, "Wrapped twice", "smime-opaque-in-opaque"),
		cms([]byte(innerOpaque), cmsOpts{attached: true, certs: chain, signers: []*pair{alice}})))

	// Broken ASN.1: truncated, endless nesting, a huge eContent length.
	good := signClear(alice, textEntity, cmsOpts{certs: chain})
	write("smime-asn1-truncated.eml", clearSigned(headers(from, "Signed report", "smime-asn1-truncated"), textEntity, good[:len(good)/2]))
	deep := bytes.Repeat([]byte{0x30, 0x80}, 5000)
	write("smime-asn1-deep.eml", clearSigned(headers(from, "Signed report", "smime-asn1-deep"), textEntity, deep))
	huge := []byte{0x30, 0x84, 0x7f, 0xff, 0xff, 0xff, 0x06, 0x09, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x07, 0x02}
	write("smime-asn1-huge.eml", opaque(headers(from, "Opaque signed report", "smime-asn1-huge"), huge))

	// --- OpenPGP ---
	pa := pgpEntity("Alice Example", "alice@example.org")
	check(os.WriteFile(filepath.Join(sigDir, "alice-pgp.asc"), []byte(pgpPublic(pa)), 0o644))
	pgpText := "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 7bit\r\n\r\nHello Bob,\r\n\r\nsigned with OpenPGP.\r\n\r\nAlice\r\n"
	write("pgp-valid.eml", pgpSigned(headers(from, "OpenPGP signed", "pgp-valid", autocrypt("alice@example.org", pa)), pgpText, pgpSign(pa, pgpText)))
	write("pgp-tampered.eml", pgpSigned(headers(from, "OpenPGP signed", "pgp-tampered"), strings.Replace(pgpText, "signed", "s1gned", 1), pgpSign(pa, pgpText)))
	write("pgp-lf.eml", strings.ReplaceAll(pgpSigned(headers(from, "OpenPGP signed", "pgp-lf"), pgpText, pgpSign(pa, pgpText)), "\r\n", "\n"))
	write("pgp-mismatch.eml", pgpSigned(headers("Bank <security@bank.example>", "OpenPGP signed", "pgp-mismatch"), pgpText, pgpSign(pa, pgpText)))

	// A key nobody has.
	stranger := pgpEntity("Stranger", "stranger@example.com")
	write("pgp-unknown-key.eml", pgpSigned(headers("Stranger <stranger@example.com>", "OpenPGP signed", "pgp-unknown-key"), pgpText, pgpSign(stranger, pgpText)))

	// Autocrypt for another address than From, and in list mail.
	write("pgp-autocrypt-other-addr.eml", pgpSigned(headers(from, "OpenPGP signed", "pgp-autocrypt-other-addr", autocrypt("eve@example.com", stranger)), pgpText, pgpSign(pa, pgpText)))
	write("pgp-autocrypt-bulk.eml", headers(from, "Newsletter", "pgp-autocrypt-bulk", "List-Id: <news.example.org>",
		"List-Unsubscribe: <https://example.org/u>", "Precedence: bulk", autocrypt("alice@example.org", pa))+
		"Content-Type: text/plain\r\n\r\nNews.\r\n")

	// Inline signature.
	var inline bytes.Buffer
	w, err := openpgpClearsign(&inline, pa, "Hello Bob,\nthis is signed inline.\n")
	check(err)
	_ = w
	write("pgp-inline.eml", headers(from, "Inline signed", "pgp-inline")+"Content-Type: text/plain; charset=utf-8\r\n\r\n"+
		strings.ReplaceAll(inline.String(), "\n", "\r\n"))

	// A revoked key (revoked before the message arrived).
	rev := pgpEntity("Robert Revoked", "robert@example.org")
	revText := pgpText
	revSig := pgpSign(rev, revText)
	check(rev.RevokeKey(packet.KeyCompromised, "lost", &packet.Config{Time: func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }}))
	check(os.WriteFile(filepath.Join(sigDir, "robert-revoked.asc"), []byte(pgpPublic(rev)), 0o644))
	write("pgp-revoked-key.eml", pgpSigned(headers("Robert Revoked <robert@example.org>", "OpenPGP signed", "pgp-revoked-key"), revText, revSig))

	// keyChanged: a second key for Carol.
	c1 := pgpEntity("Carol Example", "carol@example.org")
	c2 := pgpEntity("Carol Example", "carol@example.org")
	check(os.WriteFile(filepath.Join(sigDir, "carol-1.asc"), []byte(pgpPublic(c1)), 0o644))
	check(os.WriteFile(filepath.Join(sigDir, "carol-2.asc"), []byte(pgpPublic(c2)), 0o644))
	write("pgp-key-changed.eml", pgpSigned(headers("Carol Example <carol@example.org>", "OpenPGP signed", "pgp-key-changed", autocrypt("carol@example.org", c2)), pgpText, pgpSign(c2, pgpText)))

	// The sender's key attached.
	dave := pgpEntity("Dave Example", "dave@example.org")
	attached := "Content-Type: multipart/mixed; boundary=\"key-b\"\r\n\r\n--key-b\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nMy key is attached.\r\n" +
		"--key-b\r\nContent-Type: application/pgp-keys; name=\"dave.asc\"\r\nContent-Disposition: attachment; filename=\"dave.asc\"\r\n\r\n" +
		strings.ReplaceAll(pgpPublic(dave), "\n", "\r\n") + "\r\n--key-b--\r\n"
	write("pgp-attached-key.eml", pgpSigned(headers("Dave Example <dave@example.org>", "OpenPGP signed", "pgp-attached-key"), attached, pgpSign(dave, attached)))

	// A user ID with bidi and control characters.
	bidiKey := pgpEntity("Dave ‮naD‬\u0007", "dave@example.org")
	check(os.WriteFile(filepath.Join(sigDir, "dave-bidi.asc"), []byte(pgpPublic(bidiKey)), 0o644))
	write("pgp-bidi-uid.eml", pgpSigned(headers("Dave Example <dave@example.org>", "OpenPGP signed", "pgp-bidi-uid"), pgpText, pgpSign(bidiKey, pgpText)))

	fmt.Println("signature test data written")
}

func openpgpClearsign(w *bytes.Buffer, e *openpgp.Entity, text string) (int, error) {
	pt, err := clearsign.Encode(w, e.PrivateKey, nil)
	if err != nil {
		return 0, err
	}
	n, err := pt.Write([]byte(text))
	if err != nil {
		return n, err
	}
	return n, pt.Close()
}

package jws

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lestrrat/go-jwx/buffer"
	"github.com/lestrrat/go-jwx/jwa"
	"github.com/stretchr/testify/assert"
)

func TestRsaSign_NewRsaSignWithBadAlgorithm(t *testing.T) {
	_, err := NewRsaSign(jwa.SignatureAlgorithm("FooBar"), nil)
	if !assert.Equal(t, ErrUnsupportedAlgorithm, err, "Unknown algorithm should return error") {
		return
	}
}

func TestRsaSign_SignWithBadAlgorithm(t *testing.T) {
	s := &RsaSign{
		Algorithm: jwa.SignatureAlgorithm("FooBar"),
	}

	_, err := s.Sign([]byte{'a', 'b', 'c'})
	if !assert.Equal(t, ErrUnsupportedAlgorithm, err, "Sign with unknown algorithm should return error") {
		return
	}
}

func TestRsaSign_SignWithNoPrivateKey(t *testing.T) {
	s := &RsaSign{
		Algorithm: jwa.RS256,
	}

	_, err := s.Sign([]byte{'a', 'b', 'c'})
	if !assert.Equal(t, ErrMissingPrivateKey, err, "Sign with no private key should return error") {
		return
	}
}

func TestRsaSign_VerifyWithNoPrivateKey(t *testing.T) {
	s := &RsaSign{
		Algorithm: jwa.RS256,
	}

	err := s.Verify([]byte{'a', 'b', 'c'}, []byte{'d', 'e', 'f'})
	if !assert.Equal(t, ErrMissingPublicKey, err, "Verify with no private key should return error") {
		return
	}
}

func TestMultiSigner(t *testing.T) {
	rsakey, err := rsa.GenerateKey(rand.Reader, 2048)
	if !assert.NoError(t, err, "RSA key generated") {
		return
	}

	dsakey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if !assert.NoError(t, err, "ECDSA key generated") {
		return
	}

	ms := &MultiSign{}

	s1, err := NewRsaSign(jwa.RS256, rsakey)
	if !assert.NoError(t, err, "RSA Signer created") {
		return
	}
	s1.KeyID = "2010-12-29"
	ms.AddSigner(s1)

	s2, err := NewEcdsaSign(jwa.ES256, dsakey)
	if !assert.NoError(t, err, "DSA Signer created") {
		return
	}
	s2.KeyID = "e9bc097a-ce51-4036-9562-d2ade882db0d"
	ms.AddSigner(s2)

	v := strings.Join([]string{`{"iss":"joe",`, ` "exp":1300819380,`, ` "http://example.com/is_root":true}`}, "\r\n")
	m, err := ms.MultiSign(buffer.Buffer(v))
	if !assert.NoError(t, err, "MultiSign succeeded") {
		return
	}

	jsonbuf, _ := json.MarshalIndent(m, "", "  ")
	t.Logf("%s", jsonbuf)
}
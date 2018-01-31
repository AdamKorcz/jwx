//go:generate go run internal/cmd/genheader/main.go

// Package jws implements the digital signature on JSON based data
// structures as described in https://tools.ietf.org/html/rfc7515
//
// If you do not care about the details, the only things that you
// would need to use are the following functions:
//
//     jws.Sign(payload, algorithm, key)
//     jws.Verify(encodedjws, algorithm, key)
//
// To sign, simply use `jws.Sign`. `payload` is a []byte buffer that
// contains whatever data you want to sign. `alg` is one of the
// jwa.SignatureAlgorithm constants from package jwa. For RSA and
// ECDSA family of algorithms, you will need to prepare a private key.
// For HMAC family, you just need a []byte value. The `jws.Sign`
// function will return the encoded JWS message on success.
//
// To verify, use `jws.Verify`. It will parse the `encodedjws` buffer
// and verify the result using `algorithm` and `key`. Upon successful
// verification, the original payload is returned, so you can work on it.
package jws

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"unicode"

	"github.com/lestrrat/go-jwx/buffer"
	"github.com/lestrrat/go-jwx/jwa"
	"github.com/lestrrat/go-jwx/jwk"
	"github.com/lestrrat/go-jwx/jws/sign"
	"github.com/lestrrat/go-jwx/jws/verify"
	pdebug "github.com/lestrrat/go-pdebug"
	"github.com/pkg/errors"
)

// Sign is a short way to generate a JWS in compact serialization
// for a given payload. If you need more control over the signature
// generation process, you should manually create signers and tweak
// the message.
/*
func Sign(payload []byte, alg jwa.SignatureAlgorithm, key interface{}, options ...Option) ([]byte, error) {
	signer, err := sign.New(alg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create signer")
	}

	msg, err := SignMulti(payload, WithSigner(signer, key))
	if err != nil {
		return nil, errors.Wrap(err, "failed to sign payload")
	}
	return msg, nil
}
*/

type payloadSigner struct {
	signer sign.Signer
	key    interface{}
}

func (s *payloadSigner) Sign(payload []byte) ([]byte, error) {
	return s.signer.Sign(payload, s.key)
}

func (s *payloadSigner) Algorithm() jwa.SignatureAlgorithm {
	return s.signer.Algorithm()
}

// Sign generates a signature for the given payload, and serializes
// it in compact serialization format. In this format you may NOT use
// multiple signers.
//
// If you would like to pass custom headers, use the WithHeaders option.
func Sign(payload []byte, alg jwa.SignatureAlgorithm, key interface{}, options ...Option) ([]byte, error) {
	var hdrs HeaderInterface = &StandardHeaders{}
	for _, o := range options {
		switch o.Name() {
		case optkeyHeaders:
			hdrs = o.Value().(HeaderInterface)
		}
	}

	signer, err := sign.New(alg)
	if err != nil {
		return nil, errors.Wrap(err, `failed to create signer`)
	}

	hdrs.Set(AlgorithmKey, signer.Algorithm())

	hdrbuf, err := json.Marshal(hdrs)
	if err != nil {
		return nil, errors.Wrap(err, `failed to marshal headers`)
	}

	var buf bytes.Buffer
	enc := base64.NewEncoder(base64.RawURLEncoding, &buf)
	if _, err := enc.Write(hdrbuf); err != nil {
		return nil, errors.Wrap(err, `failed to write headers as base64`)
	}
	if err := enc.Close(); err != nil {
		return nil, errors.Wrap(err, `failed to finalize writing headers as base64`)
	}

	buf.WriteByte('.')
	enc = base64.NewEncoder(base64.RawURLEncoding, &buf)
	if _, err := enc.Write(payload); err != nil {
		return nil, errors.Wrap(err, `failed to write payload as base64`)
	}
	if err := enc.Close(); err != nil {
		return nil, errors.Wrap(err, `failed to finalize writing payload as base64`)
	}

	signature, err := signer.Sign(buf.Bytes(), key)
	if err != nil {
		return nil, errors.Wrap(err, `failed to sign payload`)
	}

	buf.WriteByte('.')
	enc = base64.NewEncoder(base64.RawURLEncoding, &buf)
	if _, err := enc.Write(signature); err != nil {
		return nil, errors.Wrap(err, `failed to write signature as base64`)
	}
	if err := enc.Close(); err != nil {
		return nil, errors.Wrap(err, `failed to finalize writing signature as base64`)
	}

	return buf.Bytes(), nil
}

// SignMulti accepts multiple signers via the options parameter,
// and creates a JWS in JSON serialization format that contains
// signatures from applying aforementioned signers.
func SignMulti(payload []byte, options ...Option) ([]byte, error) {
	var signers []PayloadSigner
	for _, o := range options {
		switch o.Name() {
		case optkeyPayloadSigner:
			signers = append(signers, o.Value().(PayloadSigner))
		}
	}

	if len(signers) == 0 {
		return nil, errors.New(`no signers provided`)
	}

	type signPayload struct {
		Protected []byte `json:"protected"`
		Signature []byte `json:"signature"`
	}
	var result struct {
		Payload    []byte        `json:"payload"`
		Signatures []signPayload `json:"signatures"`
	}

	encodedPayloadLen := base64.RawURLEncoding.EncodedLen(len(payload))
	encodedPayload := make([]byte, encodedPayloadLen)
	base64.RawURLEncoding.Encode(encodedPayload, payload)

	result.Payload = encodedPayload

	for _, signer := range signers {
		var hdr StandardHeaders
		hdr.Set(AlgorithmKey, signer.Algorithm())

		hdrbuf, err := json.Marshal(hdr)
		if err != nil {
			return nil, errors.Wrap(err, `failed to marshal headers`)
		}

		encodedHeaderLen := base64.RawURLEncoding.EncodedLen(len(hdrbuf))
		encodedHeader := make([]byte, encodedHeaderLen)
		base64.RawURLEncoding.Encode(encodedHeader, hdrbuf)

		var buf bytes.Buffer
		buf.Write(encodedHeader)
		buf.WriteByte('.')
		buf.Write(encodedPayload)
		signature, err := signer.Sign(buf.Bytes())
		if err != nil {
			return nil, errors.Wrap(err, `failed to sign payload`)
		}
		encodedSignatureLen := base64.RawURLEncoding.EncodedLen(len(signature))
		encodedSignature := make([]byte, encodedSignatureLen)
		base64.RawURLEncoding.Encode(encodedSignature, signature)

		result.Signatures = append(result.Signatures, signPayload{
			Protected: encodedHeader,
			Signature: encodedSignature,
		})
	}

	return json.Marshal(result)
}

type EncodedSignature struct {
	Protected []byte `json:"protected,omitempty"`
	Headers []byte `json:"header,omitempty"`
	Signature []byte `json:"signature,omitempty"`
}

type EncodedMessage struct {
	Payload []byte `json:"payload"`
	Signatures []*EncodedSignature `json:"signatures,omitempty"`
}

type FullEncodedMessage struct {
	*EncodedSignature // embedded to pick up flattened JSON message
	*EncodedMessage
}

// Verify checks if the given JWS message is verifiable using `alg` and `key`.
// If the verification is successful, `err` is nil, and the content of the
// payload that was signed is returned. If you need more fine-grained
// control of the verification process, manually call `Parse`, generate a
// verifier, and call `Verify` on the parsed JWS message object.
func Verify(buf []byte, alg jwa.SignatureAlgorithm, key interface{}) (ret []byte, err error) {
	if pdebug.Enabled {
		g := pdebug.Marker("jws.Verify").BindError(&err)
		defer g.End()
	}

	verifier, err := verify.New(alg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create verifier")
	}

	buf = bytes.TrimSpace(buf)
	if len(buf) == 0 {
		return nil, errors.New(`empty buffer`)
	}

	if buf[0] == '{' {
		if pdebug.Enabled {
			pdebug.Printf("verifying in JSON mode")
		}

		var v FullEncodedMessage
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, errors.Wrap(err, `failed to unmarshal JWS message`)
		}

		// There's something wrong if the Message part is not initialized
		if v.EncodedMessage == nil {
			return nil, errors.Wrap(err, `invalid JWS message format`)
		}

		// if we're using the flattened serialization format, then m.Signature
		// will be non-nil
		msg := v.EncodedMessage
		if v.EncodedSignature != nil {
			msg.Signatures[0] = v.EncodedSignature
		}

		var buf bytes.Buffer
		for _, sig := range msg.Signatures {
			buf.Reset()
			buf.Write(sig.Protected)
			buf.WriteByte('.')
			buf.Write(msg.Payload)
			if pdebug.Enabled {
				pdebug.Printf("protected %s", sig.Protected)
				pdebug.Printf("payload %s", msg.Payload)
				pdebug.Printf("signature %s", sig.Signature)
			}
			decodedSignature := make([]byte, base64.RawURLEncoding.DecodedLen(len(sig.Signature)))
			if _, err := base64.RawURLEncoding.Decode(decodedSignature, sig.Signature); err != nil {
				continue
			}

			if err := verifier.Verify(buf.Bytes(), decodedSignature, key); err == nil {
				// verified!
				decodedPayloadLen := base64.RawURLEncoding.DecodedLen(len(msg.Payload))
				decodedPayload := make([]byte, decodedPayloadLen)
				if _, err := base64.RawURLEncoding.Decode(decodedPayload, msg.Payload); err != nil {
					return nil, errors.Wrap(err, `message verified, failed to decode payload`)
				}
				return decodedPayload, nil
			}
		}
		return nil, errors.New(`could not verify with any of the signatures`)
	}

	protected, payload, signature, err := SplitCompact(bytes.NewReader(buf))
	if err != nil {
		return nil, errors.Wrap(err, `failed extract from compact serialization format`)
	}

	if pdebug.Enabled {
		pdebug.Printf("protected = %s", protected)
		pdebug.Printf("payload = %s", payload)
		pdebug.Printf("signature = %s", signature)
	}

	var verifyBuf bytes.Buffer
	verifyBuf.Write(protected)
	verifyBuf.WriteByte('.')
	verifyBuf.Write(payload)

	decodedSignature := make([]byte, base64.RawURLEncoding.DecodedLen(len(signature)))
	if _, err := base64.RawURLEncoding.Decode(decodedSignature, signature); err != nil {
		return nil, errors.Wrap(err, `failed to decode signature`)
	}
	if err := verifier.Verify(verifyBuf.Bytes(), decodedSignature, key); err != nil {
		return nil, errors.Wrap(err, `failed to verify message`)
	}

	decodedPayload := make([]byte, base64.RawURLEncoding.DecodedLen(len(payload)))
	if _, err := base64.RawURLEncoding.Decode(decodedPayload, payload); err != nil {
		return nil, errors.Wrap(err, `message verified, failed to decode payload`)
	}
	return decodedPayload, nil
}

// VerifyWithJKU verifies the JWS message using a remote JWK
// file represented in the url.
func VerifyWithJKU(buf []byte, jwkurl string) ([]byte, error) {
	key, err := jwk.FetchHTTP(jwkurl)
	if err != nil {
		return nil, errors.Wrap(err, `failed to fetch jwk via HTTP`)
	}

	return VerifyWithJWKSet(buf, key, nil)
}

// VerifyWithJWK verifies the JWS message using the specified JWK
func VerifyWithJWK(buf []byte, key jwk.Key) (payload []byte, err error) {
	if pdebug.Enabled {
		g := pdebug.Marker("jws.VerifyWithJWK").BindError(&err)
		defer g.End()
	}

	keyval, err := key.Materialize()
	if err != nil {
		return nil, errors.Wrap(err, `failed to materialize jwk.Key`)
	}

	payload, err = Verify(buf, jwa.SignatureAlgorithm(key.Alg()), keyval)
	if err != nil {
		return nil, errors.Wrap(err, "failed to verify message")
	}
	return payload, nil
}

// VerifyWithJWKSet verifies the JWS message using JWK key set.
// By default it will only pick up keys that have the "use" key
// set to either "sig" or "enc", but you can override it by
// providing a keyaccept function.
func VerifyWithJWKSet(buf []byte, keyset *jwk.Set, keyaccept JWKAcceptFunc) (payload []byte, err error) {
	if pdebug.Enabled {
		g := pdebug.Marker("jws.VerifyWithJWKSet").BindError(&err)
		defer g.End()
	}
	if keyaccept == nil {
		keyaccept = DefaultJWKAcceptor
	}

	for _, key := range keyset.Keys {
		if !keyaccept(key) {
			continue
		}

		payload, err := VerifyWithJWK(buf, key)
		if err == nil {
			return payload, nil
		}
	}

	return nil, errors.New("failed to verify with any of the keys")
}

// Parse parses contents from the given source and creates a jws.Message
// struct. The input can be in either compact or full JSON serialization.
func Parse(src io.Reader) (m *Message, err error) {
	if pdebug.Enabled {
		g := pdebug.Marker("jws.Parse").BindError(&err)
		defer g.End()
	}

	rdr := bufio.NewReader(src)
	var first rune
	for {
		r, _, err := rdr.ReadRune()
		if err != nil {
			return nil, errors.Wrap(err, `failed to read rune`)
		}
		if !unicode.IsSpace(r) {
			first = r
			rdr.UnreadRune()
			break
		}
	}

	if first == '{' {
		return parseJSON(rdr)
	}
	return parseCompact(rdr)
}

// ParseString is the same as Parse, but take in a string
func ParseString(s string) (*Message, error) {
	return Parse(strings.NewReader(s))
}

func parseJSON(src io.Reader) (*Message, error) {
	m := struct {
		*Message
		*Signature
	}{}

	if err := json.NewDecoder(src).Decode(&m); err != nil {
		return nil, errors.Wrap(err, `failed to parse jws message`)
	}

	// if the "signature" field exist, treat it as a flattened
	if m.Signature != nil {
		if len(m.Message.Signatures) != 0 {
			return nil, errors.New("invalid message: mixed flattened/full json serialization")
		}

		m.Message.Signatures = []Signature{*m.Signature}
	}

	for _, sig := range m.Message.Signatures {
		if sig.ProtectedHeader.Algorithm == "" {
			sig.ProtectedHeader.Algorithm = jwa.NoSignature
		}
	}

	return m.Message, nil
}

func scanDot(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}

	if i := bytes.IndexByte(data, '.'); i >= 0 {
		return i + 1, data[0:i], nil
	}

	// If we're at EOF, we have a final, non-terminated line. Return it.
	if atEOF {
		return len(data), data, nil
	}
	// Request more data.
	return 0, nil, nil
}

// splitCompact
func SplitCompact(rdr io.Reader) ([]byte, []byte, []byte, error) {
	var protected []byte
	var payload []byte
	var signature []byte
	var periods int
	var state int

	buf := make([]byte, 4096)
	var sofar []byte

	for {
		n, err := rdr.Read(buf)
		if n == 0 && err != nil {
			break
		}

		sofar = append(sofar, buf[:n]...)
		for loop := true; loop; {
			i := bytes.IndexByte(sofar, '.')
			switch i {
			case -1:
				l := len(sofar)
				if l <= 0 {
					loop = false
					continue
				}
				i = l
			default:
				periods++
			}

			switch state {
			case 0:
				protected = sofar[:i]
				if pdebug.Enabled {
					pdebug.Printf("header segment = %s", protected)
				}
				state++
			case 1:
				payload = sofar[:i]
				if pdebug.Enabled {
					pdebug.Printf("payload segment = %s", payload)
				}
				state++
			case 2:
				signature = sofar[:i]
				if pdebug.Enabled {
					pdebug.Printf("signature segment = %s", signature)
				}
			}
			if len(sofar) <= i {
				sofar = []byte(nil)
			} else {
				sofar = sofar[i+1:]
			}
		}
	}
	if periods != 2 {
		return nil, nil, nil, errors.New(`invalid number of segments`)
	}

	return protected, payload, signature, nil
}

// parseCompact parses a JWS value serialized via compact serialization.
func parseCompact(rdr io.Reader) (m *Message, err error) {
	if pdebug.Enabled {
		g := pdebug.Marker("jws.Parse (compact)").BindError(&err)
		defer g.End()
	}

	protected, payload, signature, err := SplitCompact(rdr)
	if err != nil {
		return nil, errors.Wrap(err, `invalid compact serialization format`)
	}

	decodedHeader := make([]byte, base64.RawURLEncoding.DecodedLen(len(protected)))
	if _, err := base64.RawURLEncoding.Decode(decodedHeader, protected); err != nil {
		return nil, errors.Wrap(err, `failed to decode headers`)
	}

	var hdr = &EncodedHeader{Header: NewHeader()}
	if err := json.Unmarshal(decodedHeader, hdr.Header); err != nil {
		return nil, errors.Wrap(err, `failed to parse JOSE headers`)
	}
	hdr.Source = protected

	decodedPayload := make([]byte, base64.RawURLEncoding.DecodedLen(len(payload)))
	if _, err = base64.RawURLEncoding.Decode(decodedPayload, payload); err != nil {
		return nil, errors.Wrap(err, `failed to decode payload`)
	}

	decodedSignature := make([]byte, base64.RawURLEncoding.DecodedLen(len(signature)))
	if _, err := base64.RawURLEncoding.Decode(decodedSignature, signature); err != nil {
		return nil, errors.Wrap(err, `failed to decode signature`)
	}

	var buf bytes.Buffer
	buf.Write(protected)
	buf.WriteByte('.')
	buf.Write(payload)

	s := NewSignature()
	s.Signature = decodedSignature
	s.ProtectedHeader = hdr
	return &Message{
		//	source:     buf.Bytes(),
		Payload:    buffer.Buffer(decodedPayload),
		Signatures: []Signature{*s},
	}, nil
}

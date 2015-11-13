package jws

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/lestrrat/go-jwx/buffer"
)

func (s CompactSerialize) Serialize(m *Message) ([]byte, error) {
	if len(m.Signatures) != 1 {
		return nil, errors.New("wrong number of signatures for compact serialization")
	}

	signature := m.Signatures[0]

	hdr := NewHeader()
	if err := hdr.Copy(signature.ProtectedHeader.Header); err != nil {
		return nil, err
	}
	hdr, err := hdr.Merge(signature.PublicHeader)
	if err != nil {
		return nil, err
	}

	hdrbuf, err := hdr.Base64Encode()
	if err != nil {
		return nil, err
	}

	b64payload, err := m.Payload.Base64Encode()
	if err != nil {
		return nil, err
	}
	b64signature, err := buffer.Buffer(signature.Signature).Base64Encode()
	if err != nil {
		return nil, err
	}
	buf := bytes.Join(
		[][]byte{
			hdrbuf,
			b64payload,
			b64signature,
		},
		[]byte{'.'},
	)

	return buf, nil
}

// Serialize converts the mssage into a JWE JSON serialize format byte buffer
func (s JSONSerialize) Serialize(m *Message) ([]byte, error) {
	if s.Pretty {
		return json.MarshalIndent(m, "", "  ")
	}
	return json.Marshal(m)
}
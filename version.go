package jsonrpc2

import (
	"encoding/json"
	"errors"
	"fmt"
)

const ProtocolVersion = "2.0"

var ErrWrongProtocolVersion = errors.New("Wrong protocol version for jsonrpc2")
var errWrongProtoVerDecode = fmt.Errorf("%w (%w)", ErrDecoding, ErrWrongProtocolVersion)

// Version represents the jsonrpc member of jsonrpc2 requests and responses.
type Version struct {
	present bool
}

// IsValid returns true if the jsonrpc member was present.
func (v *Version) IsValid() bool {
	return v.present
}

func (v *Version) UnmarshalJSON(data []byte) error {
	var str string

	if err := json.Unmarshal(data, &str); err != nil {
		return fmt.Errorf("%w (%w)", ErrDecoding, err)
	}

	if str != ProtocolVersion {
		return errWrongProtoVerDecode
	}

	v.present = true

	return nil
}

func (Version) MarshalJSON() ([]byte, error) {
	buf, err := json.Marshal(string(ProtocolVersion))

	if err != nil {
		return nil, fmt.Errorf("%w (%w)", ErrEncoding, err)
	}

	return buf, nil
}

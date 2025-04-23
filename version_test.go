package jsonrpc2

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersion_IsValid(t *testing.T) {
	t.Parallel()

	var v Version

	// Initially, it should be invalid
	assert.False(t, v.IsValid(), "Initial version should be invalid")

	// Unmarshal valid data
	err := json.Unmarshal([]byte(`"2.0"`), &v)
	require.NoError(t, err, "Unmarshal should succeed for valid version")
	assert.True(t, v.IsValid(), "Version should be valid after successful unmarshal")

	// Reset and unmarshal invalid data
	v = Version{}
	err = json.Unmarshal([]byte(`"1.0"`), &v)
	require.Error(t, err, "Unmarshal should fail for invalid version")
	assert.False(t, v.IsValid(), "Version should remain invalid after failed unmarshal")
}

func TestVersion_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	//nolint:govet //Do not reorder struct
	tests := []struct {
		name        string
		input       string
		expectErr   bool
		expectedErr error
		expectValid bool
	}{
		{
			name:        "Valid version",
			input:       `"2.0"`,
			expectErr:   false,
			expectValid: true,
		},
		{
			name:        "Incorrect version",
			input:       `"1.0"`,
			expectErr:   true,
			expectedErr: ErrWrongProtocolVersion,
			expectValid: false,
		},
		{
			name:        "Invalid JSON type (number)",
			input:       `2.0`,
			expectErr:   true,
			expectedErr: ErrDecoding,
			expectValid: false,
		},
		{
			name:        "Invalid JSON syntax",
			input:       `"2.0`, // Missing closing quote
			expectErr:   true,
			expectedErr: &json.SyntaxError{},
			expectValid: false,
		},
		{
			name:        "Empty input",
			input:       ``,
			expectErr:   true,
			expectedErr: &json.SyntaxError{},
			expectValid: false,
		},
		{
			name:        "Null input",
			input:       `null`,
			expectErr:   true, // json.Unmarshal treats null as a valid string "null" initially
			expectedErr: ErrWrongProtocolVersion,
			expectValid: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var v Version
			err := json.Unmarshal([]byte(tc.input), &v)

			if tc.expectErr {
				require.Error(t, err, "Expected an error")

				if tc.expectedErr != nil {
					// json.SyntaxError is annoying to test
					synErr := &json.SyntaxError{}
					syntaxError := &json.SyntaxError{}

					if errors.As(tc.expectedErr, &syntaxError) {
						if errors.As(err, &synErr) {
							return
						}
					}

					assert.ErrorIs(t, err, tc.expectedErr, "Expected specific error")
				}
			} else {
				require.NoError(t, err, "Expected no error")
			}

			assert.Equal(t, tc.expectValid, v.IsValid(), "Validity expectation mismatch")
		})
	}
}

func TestVersion_MarshalJSON(t *testing.T) {
	t.Parallel()

	var v Version

	// Need to unmarshal first to set the internal state correctly
	err := json.Unmarshal([]byte(`"2.0"`), &v)
	require.NoError(t, err, "Setup unmarshal failed")
	require.True(t, v.IsValid(), "Version should be valid for marshal test")

	b, err := json.Marshal(v)
	require.NoError(t, err, "Marshal should succeed")
	assert.JSONEq(t, `"2.0"`, string(b), "Marshalled JSON should be '\"2.0\"'")

	// Test marshalling a zero value (should still marshal correctly as per MarshalJSON implementation)
	var zeroV Version
	bZero, errZero := json.Marshal(zeroV)
	require.NoError(t, errZero, "Marshal zero value should succeed")
	assert.JSONEq(t, `"2.0"`, string(bZero), "Marshalled zero value JSON should be '\"2.0\"'")
}

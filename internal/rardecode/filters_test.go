package rardecode

import (
	"errors"
	"testing"
)

func TestVMFilterOutOfRangeOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		start  uint32
		length uint32
	}{
		{name: "start_plus_length_past_vm", start: vmSize - 1, length: 2},
		{name: "uint32_wrap", start: vmMask, length: vmMask},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &vmFilter{
				code: []command{
					{f: mov, op: []operand{opD(vmGlobalAddr + 0x20), opI(tt.start)}},
					{f: mov, op: []operand{opD(vmGlobalAddr + 0x1c), opI(tt.length)}},
				},
			}
			_, err := f.execute(nil, nil, []byte("x"), 0)
			if !errors.Is(err, ErrInvalidFilter) {
				t.Fatalf("execute: %v, want ErrInvalidFilter", err)
			}
		})
	}
}

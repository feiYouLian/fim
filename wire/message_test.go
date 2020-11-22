package wire

import (
	"testing"
)

func TestHeader_GetSequence(t *testing.T) {
	tests := []struct {
		name string
		h    Header
		want uint16
	}{
		// TODO: Add test cases.
		{"def", Header{byte(PIDLogin), 0, 1, 0, 2, 0, 0}, uint16(1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.h.GetSequence(); got != tt.want {
				t.Errorf("GetSequence() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsProtocol(t *testing.T) {
	type args struct {
		id PID
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		// TODO: Add test cases.
		{"login", args{PIDChat}, true},
		{"login", args{PID(255)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsProtocol(tt.args.id); got != tt.want {
				t.Errorf("IsProtocol() = %v, want %v", got, tt.want)
			}
		})
	}
}

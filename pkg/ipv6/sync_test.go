package ipv6

import (
	"net"
	"testing"
)

func TestApplyIPv6Suffix(t *testing.T) {
	tests := []struct {
		name     string
		resolved string
		suffix   string
		want     string
		wantErr  bool
	}{
		{
			name:     "standard suffix with /64",
			resolved: "2a01:cb04:6a8:5100::1",
			suffix:   "::166/64",
			want:     "2a01:cb04:6a8:5100::166",
		},
		{
			name:     "empty suffix returns resolved",
			resolved: "2a01:cb04:6a8:5100::1",
			suffix:   "",
			want:     "2a01:cb04:6a8:5100::1",
		},
		{
			name:     "suffix with /128",
			resolved: "2a01:cb04:6a8:5100::1",
			suffix:   "::43/128",
			want:     "2a01:cb04:6a8:5100::43",
		},
		{
			name:     "different prefix from ISP",
			resolved: "2a01:cb04:abc:def0::1",
			suffix:   "::166/64",
			want:     "2a01:cb04:abc:def0::166",
		},
		{
			name:     "suffix with multiple non-zero parts",
			resolved: "2a01:cb04:6a8:5100::1",
			suffix:   "::1:0:0:166/64",
			want:     "2a01:cb04:6a8:5100:1::166",
		},
		{
			name:     "invalid suffix",
			resolved: "2a01:cb04:6a8:5100::1",
			suffix:   "not-valid",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved := net.ParseIP(tt.resolved)
			if resolved == nil {
				t.Fatalf("invalid test resolved IP: %s", tt.resolved)
			}

			got, err := applyIPv6Suffix(resolved, tt.suffix)
			if (err != nil) != tt.wantErr {
				t.Errorf("applyIPv6Suffix() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("applyIPv6Suffix() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestManagedKeysMatch(t *testing.T) {
	tests := []struct {
		name     string
		existing map[string]string
		newData  map[string]string
		want     bool
	}{
		{
			name:     "all managed keys match",
			existing: map[string]string{"GATEWAY_IPV6": "2a01::166", "GATEWAY_IPV6_SUFFIX": "::166/64"},
			newData:  map[string]string{"GATEWAY_IPV6": "2a01::166", "GATEWAY_IPV6_SUFFIX": "::166/64"},
			want:     true,
		},
		{
			name:     "managed keys match with extra existing keys",
			existing: map[string]string{"GATEWAY_IPV6": "2a01::166", "GATEWAY_IPV6_SUFFIX": "::166/64", "OTHER_KEY": "value"},
			newData:  map[string]string{"GATEWAY_IPV6": "2a01::166", "GATEWAY_IPV6_SUFFIX": "::166/64"},
			want:     true,
		},
		{
			name:     "managed key value differs",
			existing: map[string]string{"GATEWAY_IPV6": "2a01::166"},
			newData:  map[string]string{"GATEWAY_IPV6": "2a01::167"},
			want:     false,
		},
		{
			name:     "managed key missing from existing",
			existing: map[string]string{},
			newData:  map[string]string{"GATEWAY_IPV6": "2a01::166"},
			want:     false,
		},
		{
			name:     "nil existing map",
			existing: nil,
			newData:  map[string]string{"GATEWAY_IPV6": "2a01::166"},
			want:     false,
		},
		{
			name:     "empty new data always matches",
			existing: map[string]string{"GATEWAY_IPV6": "2a01::166"},
			newData:  map[string]string{},
			want:     true,
		},
		{
			name:     "both nil",
			existing: nil,
			newData:  nil,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := managedKeysMatch(tt.existing, tt.newData); got != tt.want {
				t.Errorf("managedKeysMatch() = %v, want %v", got, tt.want)
			}
		})
	}
}

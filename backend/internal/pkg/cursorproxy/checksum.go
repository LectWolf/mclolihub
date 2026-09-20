package cursorproxy

import (
	"encoding/base64"
	"time"
)

func obfuscate(buf []byte) []byte {
	last := byte(165)
	out := make([]byte, len(buf))
	for i, value := range buf {
		out[i] = ((value ^ last) + byte(i%256)) & 255
		last = out[i]
	}
	return out
}

// Checksum builds x-cursor-checksum from machineID (same algorithm as Grok Bot / sand host).
func Checksum(machineID string) string {
	ks := uint64(time.Now().UnixMilli()) / 1_000_000
	raw := []byte{
		byte(ks >> 40),
		byte(ks >> 32),
		byte(ks >> 24),
		byte(ks >> 16),
		byte(ks >> 8),
		byte(ks),
	}
	token := base64.RawURLEncoding.EncodeToString(obfuscate(raw))
	return token + machineID
}

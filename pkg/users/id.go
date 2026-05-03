package users

import (
	"crypto/rand"
	"fmt"
	"time"
)

// IDPrefix is the literal prefix on every generated User ID. PROP-001 picked
// `u_` so admin tooling can grep for user IDs unambiguously across logs.
const IDPrefix = "u_"

// crockford is RFC-style Crockford Base32 (no I/L/O/U). Same alphabet ULID
// uses; encoded inline so pkg/users carries zero deps.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewID returns a fresh user ID: u_<26-char ULID>. ULIDs are
// timestamp-prefixed and lexicographically sortable, which gives us free
// time-based ordering on `ORDER BY id` in Postgres without an extra column.
//
// Format: 48-bit unix-ms timestamp + 80-bit randomness, encoded as 26 chars
// of Crockford Base32. Inline so the public swarm repo doesn't pull
// oklog/ulid for two dozen lines of bit-shuffling.
func NewID() (string, error) {
	return newIDAt(time.Now())
}

func newIDAt(t time.Time) (string, error) {
	var id [16]byte
	ms := uint64(t.UnixMilli()) & ((1 << 48) - 1)
	id[0] = byte(ms >> 40)
	id[1] = byte(ms >> 32)
	id[2] = byte(ms >> 24)
	id[3] = byte(ms >> 16)
	id[4] = byte(ms >> 8)
	id[5] = byte(ms)
	if _, err := rand.Read(id[6:]); err != nil {
		return "", fmt.Errorf("ulid randomness: %w", err)
	}
	return IDPrefix + encodeULID(id), nil
}

// encodeULID renders 16 bytes as 26 Crockford Base32 chars. Hand-unrolled
// from the canonical oklog/ulid encode — provably correct because every
// 5-bit group is composed of explicit byte-and-shift operations rather than
// a generic bit-cursor.
func encodeULID(id [16]byte) string {
	dst := [26]byte{}
	// 10-byte timestamp portion (positions 0-15)
	dst[0] = crockford[(id[0]&224)>>5]
	dst[1] = crockford[id[0]&31]
	dst[2] = crockford[(id[1]&248)>>3]
	dst[3] = crockford[((id[1]&7)<<2)|((id[2]&192)>>6)]
	dst[4] = crockford[(id[2]&62)>>1]
	dst[5] = crockford[((id[2]&1)<<4)|((id[3]&240)>>4)]
	dst[6] = crockford[((id[3]&15)<<1)|((id[4]&128)>>7)]
	dst[7] = crockford[(id[4]&124)>>2]
	dst[8] = crockford[((id[4]&3)<<3)|((id[5]&224)>>5)]
	dst[9] = crockford[id[5]&31]
	// 16-byte randomness portion (positions 6-15)
	dst[10] = crockford[(id[6]&248)>>3]
	dst[11] = crockford[((id[6]&7)<<2)|((id[7]&192)>>6)]
	dst[12] = crockford[(id[7]&62)>>1]
	dst[13] = crockford[((id[7]&1)<<4)|((id[8]&240)>>4)]
	dst[14] = crockford[((id[8]&15)<<1)|((id[9]&128)>>7)]
	dst[15] = crockford[(id[9]&124)>>2]
	dst[16] = crockford[((id[9]&3)<<3)|((id[10]&224)>>5)]
	dst[17] = crockford[id[10]&31]
	dst[18] = crockford[(id[11]&248)>>3]
	dst[19] = crockford[((id[11]&7)<<2)|((id[12]&192)>>6)]
	dst[20] = crockford[(id[12]&62)>>1]
	dst[21] = crockford[((id[12]&1)<<4)|((id[13]&240)>>4)]
	dst[22] = crockford[((id[13]&15)<<1)|((id[14]&128)>>7)]
	dst[23] = crockford[(id[14]&124)>>2]
	dst[24] = crockford[((id[14]&3)<<3)|((id[15]&224)>>5)]
	dst[25] = crockford[id[15]&31]
	return string(dst[:])
}

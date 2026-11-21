// Package obfuscation hides sensitive label values the same way as vmagent -remoteWrite.obfuscateLabels.
//
// A value is replaced with the hex SHA-256 digest of the value, without salt. See
// https://docs.victoriametrics.com/victoriametrics/vmagent/#obfuscating-label-values and
// app/vmagent/remotewrite/obfuscate.go in VictoriaMetrics. So one value gives the same result in every
// vmgather bundle, in every run and in MDX streams from vmagent, and support can match them.
// Values from a small space (IP:port, short host names) can be recovered by brute force.
package obfuscation

import (
	"crypto/sha256"
	"encoding/hex"
)

// Value returns the obfuscated form of v: its hex SHA-256 digest.
func Value(v string) string {
	digest := sha256.Sum256([]byte(v))
	return hex.EncodeToString(digest[:])
}

// maxCachedValues bounds the digest cache of one Labels instance.
const maxCachedValues = 100_000

// Labels obfuscates the values of a fixed list of label names and caches digests of repeated values,
// like vmagent does per batch. It is not safe for concurrent use.
type Labels struct {
	names []string
	cache map[string]string
}

// NewLabels returns an obfuscator for the given label names, or nil when names is empty.
func NewLabels(names []string) *Labels {
	if len(names) == 0 {
		return nil
	}
	return &Labels{
		names: names,
		cache: make(map[string]string),
	}
}

// Names returns the label names whose values are obfuscated.
func (l *Labels) Names() []string {
	return l.names
}

// Value returns the obfuscated form of v.
func (l *Labels) Value(v string) string {
	if obfuscated, ok := l.cache[v]; ok {
		return obfuscated
	}
	if len(l.cache) >= maxCachedValues {
		clear(l.cache)
	}
	obfuscated := Value(v)
	l.cache[v] = obfuscated
	return obfuscated
}

// ValueBytes is Value for a label value read from a parsed line; a cache hit does not allocate.
func (l *Labels) ValueBytes(v []byte) string {
	if obfuscated, ok := l.cache[string(v)]; ok {
		return obfuscated
	}
	return l.Value(string(v))
}

// Apply obfuscates the listed labels of a label map in place.
func (l *Labels) Apply(labels map[string]string) {
	for _, name := range l.names {
		if v, ok := labels[name]; ok {
			labels[name] = l.Value(v)
		}
	}
}

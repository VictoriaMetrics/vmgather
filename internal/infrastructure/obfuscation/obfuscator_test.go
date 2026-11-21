package obfuscation

import (
	"strconv"
	"testing"
)

// Expected digests come from `printf '%s' <value> | shasum -a 256`, the same transformation as vmagent
// -remoteWrite.obfuscateLabels, so bundles and MDX streams give equal values.
func TestValueMatchesVmagent(t *testing.T) {
	f := func(value, want string) {
		t.Helper()
		if got := Value(value); got != want {
			t.Fatalf("Value(%q) = %q; want %q", value, got, want)
		}
	}
	f("10.0.0.1:8482", "2f9249f5668dc42a02559613d88e7f062bcfc8760285a45d4c20dc505e60d62c")
	f("vmstorage-prod", "5fa20dd963a9482cb1f8f4e25dda3e690d118b73d445fc9729591c98cde86f78")
	f("", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	f("[2001:db8::1]:8481", "84cb4733730022ac6adf4616dc6697b0917e2416aa9539cc9b7d48f93506fe6b")
	f("pod-ü", "8478bcfdb6372c8a146346237e532de5ebf9cee762fd351899d653cae9aed6cd")
}

func TestLabelsApply(t *testing.T) {
	l := NewLabels([]string{"instance", "pod"})
	labels := map[string]string{
		"__name__": "vm_rows",
		"instance": "10.0.0.1:8482",
		"job":      "vmstorage-prod",
	}
	l.Apply(labels)
	if labels["instance"] != Value("10.0.0.1:8482") {
		t.Fatalf("instance not obfuscated: %q", labels["instance"])
	}
	if labels["job"] != "vmstorage-prod" || labels["__name__"] != "vm_rows" {
		t.Fatalf("labels outside the list changed: %v", labels)
	}
	if _, ok := labels["pod"]; ok {
		t.Fatalf("missing label must not be added: %v", labels)
	}
}

func TestLabelsCacheIsBounded(t *testing.T) {
	l := NewLabels([]string{"instance"})
	for i := 0; i < maxCachedValues+10; i++ {
		v := strconv.Itoa(i)
		if got := l.ValueBytes([]byte(v)); got != Value(v) {
			t.Fatalf("cached value differs for %q", v)
		}
	}
	if len(l.cache) > maxCachedValues {
		t.Fatalf("cache has %d entries; want at most %d", len(l.cache), maxCachedValues)
	}
}

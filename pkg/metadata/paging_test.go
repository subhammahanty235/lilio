package metadata

import "testing"

func TestListOptionsLimit(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{"unset falls back to the default", 0, DefaultListLimit},
		{"negative falls back to the default", -5, DefaultListLimit},
		{"a sane value is kept", 50, 50},
		{"an absurd value is capped", MaxListLimit * 10, MaxListLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (ListOptions{Limit: tt.in}).limit(); got != tt.want {
				t.Errorf("limit() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPaginate(t *testing.T) {
	keys := []string{"a", "b", "c", "d", "e"}

	t.Run("a full listing is not truncated", func(t *testing.T) {
		got := paginate(keys, 10)
		if got.Truncated || got.NextAfter != "" || len(got.Keys) != 5 {
			t.Errorf("got %+v, want all five keys and no continuation", got)
		}
	})

	t.Run("exactly a page is not truncated", func(t *testing.T) {
		// The boundary that matters: a caller asking for 5 and receiving 5 must
		// not be told to fetch a sixth, empty page.
		got := paginate(keys, 5)
		if got.Truncated {
			t.Errorf("got Truncated=true for a listing that ended exactly on the limit")
		}
	})

	t.Run("an over-full listing continues from the last key returned", func(t *testing.T) {
		got := paginate(keys, 2)
		if !got.Truncated {
			t.Fatal("got Truncated=false with more keys remaining")
		}
		if len(got.Keys) != 2 {
			t.Errorf("got %d keys, want 2", len(got.Keys))
		}
		if got.NextAfter != "b" {
			t.Errorf("NextAfter = %q, want %q (the last key on this page)", got.NextAfter, "b")
		}
	})

	t.Run("an empty listing", func(t *testing.T) {
		got := paginate(nil, 10)
		if got.Truncated || len(got.Keys) != 0 {
			t.Errorf("got %+v, want an empty, complete page", got)
		}
	})
}

// The resume point must exclude the key it names and include anything ordering
// after it, including keys that merely extend it.
func TestPrefixSuccessorOrdering(t *testing.T) {
	after := "key-01"
	start := clientv3PrefixSuccessor(after)

	if !(start > after) {
		t.Errorf("%q should sort after %q, so the previous page's last key is not repeated", start, after)
	}
	for _, next := range []string{"key-01x", "key-02", "key-1"} {
		if !(start < next) {
			t.Errorf("%q should sort before %q, or that key would be skipped", start, next)
		}
	}
}

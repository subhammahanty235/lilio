package metadata

// anyRevision marks a write that is not conditional on the stored revision.
const anyRevision int64 = -1

// paginate cuts a sorted key list down to one page and reports whether more
// remain. Callers pass keys already filtered by prefix and already positioned
// after ListOptions.After.
func paginate(keys []string, limit int) ListResult {
	if len(keys) <= limit {
		return ListResult{Keys: keys}
	}
	page := keys[:limit]
	return ListResult{
		Keys:      page,
		NextAfter: page[len(page)-1],
		Truncated: true,
	}
}

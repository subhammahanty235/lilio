package metadata

import "errors"

// Sentinel errors returned by every MetadataStore implementation.
//
// Callers must match these with errors.Is rather than inspecting error strings,
// so that "this object does not exist" can be told apart from "the metadata
// backend is unreachable". The two need very different handling: the first is a
// 404 and a no-op delete, the second is a 500 and a retry.
var (
	ErrBucketNotFound = errors.New("bucket not found")
	ErrBucketExists   = errors.New("bucket already exists")
	ErrBucketNotEmpty = errors.New("bucket not empty")
	ErrObjectNotFound = errors.New("object not found")
)

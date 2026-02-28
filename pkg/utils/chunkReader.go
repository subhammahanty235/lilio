package utils

import "io"

// Chunk Reader's structure

type ChunkReader struct {
	reader    io.Reader
	chunkSize int
	buffer    []byte
	chunkNum  int
	done      bool
}

func NewChunkReader(r io.Reader, chunksize int) *ChunkReader {
	return &ChunkReader{
		reader:    r,
		chunkSize: chunksize,
		buffer:    make([]byte, chunksize),
		chunkNum:  0,
		done:      false,
	}
}

func (cr *ChunkReader) NextChunk() ([]byte, int, error) {
	if cr.done {
		return nil, -1, io.EOF
	}

	totalRead := 0
	for totalRead < cr.chunkSize {
		n, err := cr.reader.Read(cr.buffer[totalRead:])
		totalRead += n
		if err == io.EOF {
			cr.done = true
			break
		}

		if err != nil {
			return nil, -1, err
		}

	}

	if totalRead == 0 {
		return nil, -1, io.EOF
	}

	chunkIndex := cr.chunkNum
	cr.chunkNum++

	chunk := make([]byte, totalRead)
	copy(chunk, cr.buffer[:totalRead])

	return chunk, chunkIndex, nil

}

func (cr *ChunkReader) ChunkCount() int {
	return cr.chunkNum
}

// Reset resets the chunk reader (if underlying reader supports seeking)
func (cr *ChunkReader) Reset() {
	if seeker, ok := cr.reader.(io.Seeker); ok {
		seeker.Seek(0, io.SeekStart)
		cr.chunkNum = 0
		cr.done = false
	}
}

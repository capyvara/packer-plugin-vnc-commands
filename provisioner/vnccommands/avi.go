package vnccommands

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Classic AVI uses 32-bit RIFF sizes. Reserve room for the index before
// accepting each frame, so hitting the limit still leaves a playable file.
const maxAVIBytes = uint64(1<<32 - 1)

type aviIndex struct{ offset, size uint32 }

type aviWriter struct {
	file                   *os.File
	width, height, fps     int
	moviBytes, largestJPEG uint32
	index                  []aviIndex
}

func newAVI(path string, width, height, fps int) (*aviWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	// Recordings are artifacts: never silently replace an existing one.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	w := &aviWriter{file: f, width: width, height: height, fps: fps, moviBytes: 4}
	if _, err := f.Write(w.header(224)); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

func (w *aviWriter) WriteFrame(jpeg []byte) error {
	chunkBytes := uint64(8 + len(jpeg) + len(jpeg)%2)
	fileBytes := uint64(220) + uint64(w.moviBytes) + chunkBytes + 8 + 16*uint64(len(w.index)+1)
	if fileBytes > maxAVIBytes {
		return errors.New("MJPEG AVI recording reached the 4 GiB file limit")
	}
	chunk := riffChunk("00dc", jpeg)
	if _, err := w.file.Write(chunk); err != nil {
		// Discard a partial last frame so Close can index all complete frames.
		end := int64(220) + int64(w.moviBytes)
		return errors.Join(err, w.file.Truncate(end), seekAVI(w.file, end))
	}
	w.index = append(w.index, aviIndex{w.moviBytes, uint32(len(jpeg))})
	w.moviBytes += uint32(chunkBytes)
	if uint32(len(jpeg)) > w.largestJPEG {
		w.largestJPEG = uint32(len(jpeg))
	}
	return nil
}

func seekAVI(f *os.File, offset int64) error {
	_, err := f.Seek(offset, 0)
	return err
}

func (w *aviWriter) Close() error {
	var index bytes.Buffer
	for _, entry := range w.index {
		index.WriteString("00dc")
		writeLE(&index, uint32(0x10), entry.offset, entry.size) // key frame
	}
	_, indexErr := w.file.Write(riffChunk("idx1", index.Bytes()))
	fileBytes := uint32(220) + w.moviBytes + 8 + uint32(index.Len())
	_, headerErr := w.file.WriteAt(w.header(fileBytes), 0)
	return errors.Join(indexErr, headerErr, w.file.Close())
}

func (w *aviWriter) header(fileBytes uint32) []byte {
	var main, stream, format bytes.Buffer
	writeLE(&main, uint32(1_000_000/w.fps), uint32(uint64(w.largestJPEG)*uint64(w.fps)), uint32(0), uint32(0x10),
		uint32(len(w.index)), uint32(0), uint32(1), w.largestJPEG, uint32(w.width), uint32(w.height),
		uint32(0), uint32(0), uint32(0), uint32(0))
	stream.WriteString("vidsMJPG")
	writeLE(&stream, uint32(0), uint16(0), uint16(0), uint32(0), uint32(1), uint32(w.fps), uint32(0),
		uint32(len(w.index)), w.largestJPEG, uint32(0xffffffff), uint32(0),
		int16(0), int16(0), int16(w.width), int16(w.height))
	writeLE(&format, uint32(40), int32(w.width), int32(w.height), uint16(1), uint16(24))
	format.WriteString("MJPG")
	writeLE(&format, uint32(w.width*w.height*3), int32(0), int32(0), uint32(0), uint32(0))
	strl := append(riffChunk("strh", stream.Bytes()), riffChunk("strf", format.Bytes())...)
	hdrl := append(riffChunk("avih", main.Bytes()), riffChunk("LIST", append([]byte("strl"), strl...))...)
	var header bytes.Buffer
	header.WriteString("RIFF")
	writeLE(&header, fileBytes-8)
	header.WriteString("AVI ")
	header.Write(riffChunk("LIST", append([]byte("hdrl"), hdrl...)))
	header.WriteString("LIST")
	writeLE(&header, w.moviBytes)
	header.WriteString("movi")
	return header.Bytes()
}

func riffChunk(id string, payload []byte) []byte {
	var b bytes.Buffer
	b.WriteString(id)
	writeLE(&b, uint32(len(payload)))
	b.Write(payload)
	if len(payload)%2 != 0 {
		b.WriteByte(0)
	}
	return b.Bytes()
}

func writeLE(b *bytes.Buffer, values ...interface{}) {
	for _, value := range values {
		if err := binary.Write(b, binary.LittleEndian, value); err != nil {
			panic(fmt.Sprintf("invalid AVI header field: %v", err))
		}
	}
}

package webpchunk

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	// ChunkID is the 4-byte identifier for our custom tag chunk
	ChunkID         = "KART"
	MetadataChunkID = "KMTD"
)

// ReadTagsFromWebP reads tags from a WebP file's custom chunk
func ReadTagsFromWebP(webpPath string) ([]string, error) {
	chunkData, ok, err := ReadChunkFromWebP(webpPath, ChunkID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return []string{}, nil
	}
	return ExtractTagsFromChunk(chunkData)
}

// ReadMetadataFromWebP reads JSON metadata from a WebP file's custom chunk.
func ReadMetadataFromWebP(webpPath string) ([]byte, error) {
	chunkData, ok, err := ReadChunkFromWebP(webpPath, MetadataChunkID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return []byte{}, nil
	}
	return chunkData, nil
}

// ReadChunkFromWebP reads one custom chunk from a WebP RIFF container.
func ReadChunkFromWebP(webpPath, targetChunkID string) ([]byte, bool, error) {
	if len(targetChunkID) != 4 {
		return nil, false, fmt.Errorf("chunk id must be 4 bytes")
	}
	file, err := os.Open(webpPath)
	if err != nil {
		return nil, false, fmt.Errorf("open webp file: %w", err)
	}
	defer file.Close()

	// Read RIFF header
	var riffHeader [12]byte
	if _, err := io.ReadFull(file, riffHeader[:]); err != nil {
		return nil, false, fmt.Errorf("read RIFF header: %w", err)
	}

	// Verify RIFF header
	if string(riffHeader[0:4]) != "RIFF" {
		return nil, false, fmt.Errorf("not a RIFF file")
	}
	if string(riffHeader[8:12]) != "WEBP" {
		return nil, false, fmt.Errorf("not a WebP file")
	}

	// Read chunks until we find our custom chunk
	for {
		var chunkID [4]byte
		var chunkSize uint32

		if _, err := io.ReadFull(file, chunkID[:]); err != nil {
			if err == io.EOF {
				// End of file, chunk not found
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("read chunk id: %w", err)
		}

		if err := binary.Read(file, binary.LittleEndian, &chunkSize); err != nil {
			if err == io.EOF {
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("read chunk size: %w", err)
		}

		// Check if this is our custom chunk
		if string(chunkID[:]) == targetChunkID {
			// Read chunk data
			chunkData := make([]byte, chunkSize)
			if _, err := io.ReadFull(file, chunkData); err != nil {
				return nil, false, fmt.Errorf("read chunk data: %w", err)
			}

			return chunkData, true, nil
		}

		// Skip this chunk (chunkSize may be odd, so we need to align to 2-byte boundary)
		chunkStart, _ := file.Seek(0, io.SeekCurrent)
		alignedSize := chunkSize
		if alignedSize%2 != 0 {
			alignedSize++
		}
		if _, err := file.Seek(chunkStart+int64(alignedSize), io.SeekStart); err != nil {
			if err == io.EOF {
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("seek next chunk: %w", err)
		}
	}
}

// WriteTagsToWebP writes tags to a WebP file's custom chunk
// This function reads the entire file, inserts/updates the chunk, and writes it back
func WriteTagsToWebP(webpPath string, tags []string) error {
	tagChunkData, err := CreateTagChunk(tags)
	if err != nil {
		return fmt.Errorf("create tag chunk: %w", err)
	}
	return WriteChunkToWebP(webpPath, ChunkID, tagChunkData)
}

// WriteMetadataToWebP writes JSON metadata to a WebP file's custom chunk.
func WriteMetadataToWebP(webpPath string, metadata []byte) error {
	return WriteChunkToWebP(webpPath, MetadataChunkID, metadata)
}

// WriteChunkToWebP inserts or replaces one custom chunk in a WebP RIFF container.
func WriteChunkToWebP(webpPath, targetChunkID string, chunkData []byte) error {
	if len(targetChunkID) != 4 {
		return fmt.Errorf("chunk id must be 4 bytes")
	}
	// Read existing file
	file, err := os.Open(webpPath)
	if err != nil {
		return fmt.Errorf("open webp file: %w", err)
	}
	defer file.Close()

	// Read RIFF header
	var riffHeader [12]byte
	if _, err := io.ReadFull(file, riffHeader[:]); err != nil {
		return fmt.Errorf("read RIFF header: %w", err)
	}

	// Verify RIFF header
	if string(riffHeader[0:4]) != "RIFF" {
		return fmt.Errorf("not a RIFF file")
	}
	if string(riffHeader[8:12]) != "WEBP" {
		return fmt.Errorf("not a WebP file")
	}

	// Read file size from RIFF header
	fileSize := binary.LittleEndian.Uint32(riffHeader[4:8])

	// Read all chunks
	type chunkInfo struct {
		id     string
		size   uint32
		data   []byte
		offset int64
	}

	var chunks []chunkInfo
	currentOffset := int64(12) // After RIFF header

	for currentOffset < int64(fileSize)+8 {
		var chunkID [4]byte
		var chunkSize uint32

		if _, err := file.Seek(currentOffset, io.SeekStart); err != nil {
			break
		}

		if _, err := io.ReadFull(file, chunkID[:]); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read chunk id: %w", err)
		}

		if err := binary.Read(file, binary.LittleEndian, &chunkSize); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read chunk size: %w", err)
		}

		chunkData := make([]byte, chunkSize)
		if _, err := io.ReadFull(file, chunkData); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read chunk data: %w", err)
		}

		chunkIDStr := string(chunkID[:])
		// Skip our custom chunk if it exists (we'll add it later)
		if chunkIDStr != targetChunkID {
			chunks = append(chunks, chunkInfo{
				id:     chunkIDStr,
				size:   chunkSize,
				data:   chunkData,
				offset: currentOffset,
			})
		}

		// Move to next chunk (aligned to 2-byte boundary)
		alignedSize := chunkSize
		if alignedSize%2 != 0 {
			alignedSize++
		}
		currentOffset += 8 + int64(alignedSize) // 8 = chunkID (4) + chunkSize (4)
	}

	// Write new file
	outFile, err := os.Create(webpPath + ".tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer outFile.Close()
	defer os.Remove(webpPath + ".tmp") // Clean up on error

	// Write RIFF header (will update size later)
	if _, err := outFile.Write(riffHeader[:]); err != nil {
		return fmt.Errorf("write RIFF header: %w", err)
	}

	// Write all existing chunks
	for _, chunk := range chunks {
		if _, err := outFile.WriteString(chunk.id); err != nil {
			return fmt.Errorf("write chunk id: %w", err)
		}
		if err := binary.Write(outFile, binary.LittleEndian, chunk.size); err != nil {
			return fmt.Errorf("write chunk size: %w", err)
		}
		if _, err := outFile.Write(chunk.data); err != nil {
			return fmt.Errorf("write chunk data: %w", err)
		}
		// Align to 2-byte boundary if needed
		if chunk.size%2 != 0 {
			if _, err := outFile.Write([]byte{0}); err != nil {
				return fmt.Errorf("write padding: %w", err)
			}
		}
	}

	// Write our custom chunk
	if _, err := outFile.WriteString(targetChunkID); err != nil {
		return fmt.Errorf("write custom chunk id: %w", err)
	}
	customChunkSize := uint32(len(chunkData))
	if err := binary.Write(outFile, binary.LittleEndian, customChunkSize); err != nil {
		return fmt.Errorf("write custom chunk size: %w", err)
	}
	if _, err := outFile.Write(chunkData); err != nil {
		return fmt.Errorf("write custom chunk data: %w", err)
	}
	// Align to 2-byte boundary if needed
	if customChunkSize%2 != 0 {
		if _, err := outFile.Write([]byte{0}); err != nil {
			return fmt.Errorf("write custom chunk padding: %w", err)
		}
	}

	// Update RIFF file size
	newFileSize, err := outFile.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("seek end: %w", err)
	}
	newFileSize -= 8 // Exclude RIFF header (4) and size field (4)

	if _, err := outFile.Seek(4, io.SeekStart); err != nil {
		return fmt.Errorf("seek to size field: %w", err)
	}
	if err := binary.Write(outFile, binary.LittleEndian, uint32(newFileSize)); err != nil {
		return fmt.Errorf("update file size: %w", err)
	}

	// Close temp file
	if err := outFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	// Windows does not allow replacing a file while the source handle remains
	// open. Unix permits this, which previously hid the issue from Linux CI.
	if err := file.Close(); err != nil {
		return fmt.Errorf("close source webp before replacement: %w", err)
	}

	// Replace original file with new file
	if err := os.Rename(webpPath+".tmp", webpPath); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}

	return nil
}

// ExtractTagsFromChunk extracts tags from chunk data
// Chunk data format: comma-separated tags string (UTF-8)
func ExtractTagsFromChunk(chunkData []byte) ([]string, error) {
	if len(chunkData) == 0 {
		return []string{}, nil
	}

	// Convert to string and split by comma
	tagsStr := strings.TrimSpace(string(chunkData))
	if tagsStr == "" {
		return []string{}, nil
	}

	tags := strings.Split(tagsStr, ",")
	var result []string
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			result = append(result, tag)
		}
	}

	return result, nil
}

// CreateTagChunk creates chunk data from tags
// Chunk data format: comma-separated tags string (UTF-8)
func CreateTagChunk(tags []string) ([]byte, error) {
	if len(tags) == 0 {
		return []byte{}, nil
	}

	// Join tags with comma
	tagsStr := strings.Join(tags, ",")
	return []byte(tagsStr), nil
}

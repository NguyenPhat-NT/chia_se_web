package app

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
)

type uploadFailure struct {
	status  int
	message string
}

func (e *uploadFailure) Error() string { return e.message }

func uploadError(status int, format string, args ...any) error {
	return &uploadFailure{status: status, message: fmt.Sprintf(format, args...)}
}

func respondUploadError(w http.ResponseWriter, err error) {
	var failure *uploadFailure
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &failure):
		fail(w, failure.status, failure.message)
	case errors.As(err, &tooLarge):
		fail(w, http.StatusRequestEntityTooLarge, "Yêu cầu tải lên vượt giới hạn dung lượng.")
	default:
		fail(w, http.StatusInternalServerError, "Không thể lưu website. Hãy kiểm tra dung lượng và thử lại.")
	}
}

// uploadBudget counts physical temporary files as well as the active releases.
// ZIP archives and their expanded contents both occupy space during validation.
type uploadBudget struct {
	cfg      Config
	baseline int64
	written  int64
}

func directoryBytes(root string) (int64, error) {
	var size int64
	err := filepath.WalkDir(root, func(p string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected symlink in data directory")
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("unexpected special file in data directory")
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func (b *uploadBudget) check(add int64) error {
	if b.cfg.TotalBytes > 0 && (b.baseline > b.cfg.TotalBytes || b.written > b.cfg.TotalBytes-b.baseline-add) {
		return uploadError(http.StatusInsufficientStorage, "Dung lượng lưu trữ chung đã đạt giới hạn; hãy xóa website hoặc giảm kích thước.")
	}
	if b.cfg.MinFreeBytes > 0 {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(b.cfg.DataDir, &stat); err != nil {
			return err
		}
		available := uint64(stat.Bavail) * uint64(stat.Bsize)
		if available < uint64(b.cfg.MinFreeBytes)+uint64(add) {
			return uploadError(http.StatusInsufficientStorage, "Bộ nhớ trống không đủ; cần giữ khoảng trống cho hệ thống.")
		}
	}
	return nil
}

// copy writes small chunks, enforcing limits against actual bytes rather than
// trusting multipart Content-Length or ZIP metadata.
func (b *uploadBudget) copy(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	var total int64
	buffer := make([]byte, 32*1024)
	for {
		n, err := src.Read(buffer)
		if n > 0 {
			if int64(n) > limit-total {
				return total, uploadError(http.StatusRequestEntityTooLarge, "Nội dung website vượt giới hạn dung lượng.")
			}
			if e := b.check(int64(n)); e != nil {
				return total, e
			}
			written, e := dst.Write(buffer[:n])
			total += int64(written)
			b.written += int64(written)
			if e != nil {
				return total, e
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

func safeUploadPath(name string, directory bool) (string, error) {
	if !utf8.ValidString(name) || len(name) == 0 || len(name) > 1024 || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") {
		return "", uploadError(http.StatusBadRequest, "Đường dẫn file không hợp lệ: %q.", name)
	}
	for _, c := range name {
		if c < 32 || c == 127 {
			return "", uploadError(http.StatusBadRequest, "Tên file chứa ký tự không hợp lệ.")
		}
	}
	if directory {
		name = strings.TrimSuffix(name, "/")
	}
	segments := strings.Split(name, "/")
	if len(segments) > 32 {
		return "", uploadError(http.StatusBadRequest, "Cấu trúc thư mục quá sâu; tối đa 32 cấp đường dẫn.")
	}
	for _, segment := range segments {
		lower := strings.ToLower(segment)
		if segment == "" || segment == "." || segment == ".." || lower == ".git" || lower == ".svn" || lower == ".hg" || lower == ".env" || strings.HasPrefix(lower, ".env.") {
			return "", uploadError(http.StatusBadRequest, "Đường dẫn bị cấm: %q.", name)
		}
	}
	if path.Clean(name) != name {
		return "", uploadError(http.StatusBadRequest, "Đường dẫn file không hợp lệ: %q.", name)
	}
	return name, nil
}

func trackUploadEntries(entries map[string]bool, name string, limit int) error {
	current := ""
	for _, segment := range strings.Split(name, "/") {
		if current != "" {
			current += "/"
		}
		current += segment
		entries[current] = true
		if len(entries) > limit {
			return uploadError(http.StatusRequestEntityTooLarge, "Website có quá nhiều file hoặc thư mục.")
		}
	}
	return nil
}

type uploadedFile struct {
	name, temporary string
	size            int64
}
type preparedUpload struct {
	directory, name, slug string
	spa, spaSet           bool
	size                  int64
}

func (s *Server) prepareUpload(w http.ResponseWriter, r *http.Request, stage string, ownerAllowance int64) (preparedUpload, error) {
	var result preparedUpload
	baseline, err := directoryBytes(s.cfg.DataDir)
	if err != nil {
		return result, err
	}
	budget := uploadBudget{cfg: s.cfg, baseline: baseline}
	if err = budget.check(0); err != nil {
		return result, err
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.UploadBytes+2*1024*1024)
	reader, err := r.MultipartReader()
	if err != nil {
		return result, uploadError(http.StatusBadRequest, "Hãy gửi file bằng multipart/form-data.")
	}
	if err = os.MkdirAll(filepath.Join(stage, "raw"), 0700); err != nil {
		return result, err
	}
	var files []uploadedFile
	fields := map[string]string{}
	seen := map[string]bool{}
	entries := map[string]bool{}
	var incoming int64
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, normalizeMultipartError(err)
		}
		_, disposition, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if err != nil {
			part.Close()
			return result, uploadError(http.StatusBadRequest, "Thông tin file tải lên không hợp lệ.")
		}
		filename, isFile := disposition["filename"]
		if !isFile {
			field := part.FormName()
			if field != "name" && field != "slug" && field != "spa" && field != "mode" {
				part.Close()
				return result, uploadError(http.StatusBadRequest, "Trường tải lên không hợp lệ.")
			}
			if _, exists := fields[field]; exists {
				part.Close()
				return result, uploadError(http.StatusBadRequest, "Thông tin tải lên bị lặp.")
			}
			value, readErr := io.ReadAll(io.LimitReader(part, 4097))
			part.Close()
			if readErr != nil {
				return result, normalizeMultipartError(readErr)
			}
			if len(value) > 4096 {
				return result, uploadError(http.StatusBadRequest, "Thông tin tải lên quá dài.")
			}
			fields[field] = string(value)
			continue
		}
		name, err := safeUploadPath(filename, false)
		if part.FormName() != "files" {
			part.Close()
			return result, uploadError(http.StatusBadRequest, "Trường file tải lên không hợp lệ.")
		}
		if err != nil {
			part.Close()
			return result, err
		}
		if seen[name] {
			part.Close()
			return result, uploadError(http.StatusBadRequest, "File trùng đường dẫn: %q.", name)
		}
		seen[name] = true
		if err = trackUploadEntries(entries, name, s.cfg.MaxFiles); err != nil {
			part.Close()
			return result, err
		}
		if len(files) >= s.cfg.MaxFiles {
			part.Close()
			return result, uploadError(http.StatusRequestEntityTooLarge, "Website có quá nhiều file.")
		}
		temporary := filepath.Join(stage, "raw", strconv.Itoa(len(files)))
		out, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			part.Close()
			return result, err
		}
		size, copyErr := budget.copy(out, part, s.cfg.UploadBytes-incoming)
		if copyErr == nil {
			copyErr = out.Sync()
		}
		closeErr := out.Close()
		part.Close()
		if copyErr != nil {
			return result, normalizeMultipartError(copyErr)
		}
		if closeErr != nil {
			return result, closeErr
		}
		incoming += size
		files = append(files, uploadedFile{name, temporary, size})
	}
	if len(files) == 0 {
		return result, uploadError(http.StatusBadRequest, "Bạn chưa chọn file hoặc thư mục.")
	}
	result.name = strings.TrimSpace(fields["name"])
	result.slug = strings.ToLower(strings.TrimSpace(fields["slug"]))
	result.spa = fields["spa"] == "true" || fields["spa"] == "1"
	_, result.spaSet = fields["spa"]
	if fields["spa"] != "" && fields["spa"] != "false" && fields["spa"] != "0" && !result.spa {
		return result, uploadError(http.StatusBadRequest, "Tùy chọn SPA không hợp lệ.")
	}
	mode := fields["mode"]
	if mode == "" {
		mode = "folder"
		if len(files) == 1 {
			switch strings.ToLower(path.Ext(files[0].name)) {
			case ".zip":
				mode = "zip"
			case ".html", ".htm":
				mode = "html"
			}
		}
	}
	content := filepath.Join(stage, "content")
	if err := os.Mkdir(content, 0700); err != nil {
		return result, err
	}
	limit := s.cfg.SiteBytes
	if ownerAllowance < limit {
		limit = ownerAllowance
	}
	switch mode {
	case "zip":
		if len(files) != 1 || strings.ToLower(path.Ext(files[0].name)) != ".zip" {
			return result, uploadError(http.StatusBadRequest, "Hãy chọn đúng một file ZIP.")
		}
		result.size, err = extractZip(files[0].temporary, content, limit, s.cfg.MaxFiles, &budget)
		if err != nil {
			return result, err
		}
	case "html":
		if len(files) != 1 || (strings.ToLower(path.Ext(files[0].name)) != ".html" && strings.ToLower(path.Ext(files[0].name)) != ".htm") {
			return result, uploadError(http.StatusBadRequest, "Hãy chọn đúng một file HTML.")
		}
		result.size = files[0].size
		if result.size > limit {
			return result, uploadError(http.StatusRequestEntityTooLarge, "Website vượt hạn mức dung lượng của tài khoản hoặc mỗi website.")
		}
		if err = os.Rename(files[0].temporary, filepath.Join(content, "index.html")); err != nil {
			return result, err
		}
	case "folder":
		for _, file := range files {
			if file.size > limit-result.size {
				return result, uploadError(http.StatusRequestEntityTooLarge, "Website vượt hạn mức dung lượng của tài khoản hoặc mỗi website.")
			}
			dest := filepath.Join(content, filepath.FromSlash(file.name))
			if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
				return result, uploadError(http.StatusBadRequest, "Cấu trúc thư mục bị xung đột.")
			}
			if err = os.Rename(file.temporary, dest); err != nil {
				return result, uploadError(http.StatusBadRequest, "Cấu trúc file tải lên không hợp lệ.")
			}
			result.size += file.size
		}
	default:
		return result, uploadError(http.StatusBadRequest, "Kiểu tải lên không hợp lệ.")
	}
	result.directory, err = findWebRoot(content)
	if err != nil {
		return result, err
	}
	// Refresh the exact size, including SQLite writes from concurrent account work.
	actual, err := directoryBytes(s.cfg.DataDir)
	if err != nil {
		return result, err
	}
	if s.cfg.TotalBytes > 0 && actual > s.cfg.TotalBytes {
		return result, uploadError(http.StatusInsufficientStorage, "Dung lượng lưu trữ chung đã đạt giới hạn.")
	}
	if err = budget.check(0); err != nil {
		return result, err
	}
	return result, nil
}

func normalizeMultipartError(err error) error {
	var failure *uploadFailure
	var tooLarge *http.MaxBytesError
	if errors.As(err, &failure) || errors.As(err, &tooLarge) {
		return err
	}
	return uploadError(http.StatusBadRequest, "Dữ liệu tải lên bị lỗi hoặc kết nối đã gián đoạn.")
}

func extractZip(filename, dest string, limit int64, maxFiles int, budget *uploadBudget) (int64, error) {
	// Check central-directory records without allocating per-entry objects first.
	// A small compressed archive can otherwise exhaust router RAM with metadata.
	if err := preflightZip(filename, maxFiles); err != nil {
		return 0, err
	}
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return 0, uploadError(http.StatusBadRequest, "File ZIP không hợp lệ hoặc bị hỏng.")
	}
	defer archive.Close()
	if len(archive.File) > maxFiles {
		return 0, uploadError(http.StatusRequestEntityTooLarge, "ZIP có quá nhiều file hoặc thư mục.")
	}
	var total int64
	seen := map[string]bool{}
	entries := map[string]bool{}
	for _, entry := range archive.File {
		directory := entry.FileInfo().IsDir()
		name, err := safeUploadPath(entry.Name, directory)
		if err != nil {
			return total, err
		}
		if seen[name] {
			return total, uploadError(http.StatusBadRequest, "ZIP có đường dẫn bị lặp: %q.", name)
		}
		seen[name] = true
		if err = trackUploadEntries(entries, name, maxFiles); err != nil {
			return total, err
		}
		if entry.Flags&1 != 0 {
			return total, uploadError(http.StatusBadRequest, "ZIP có mật khẩu không được hỗ trợ.")
		}
		if entry.Mode()&os.ModeSymlink != 0 || (!entry.Mode().IsRegular() && !directory) {
			return total, uploadError(http.StatusBadRequest, "ZIP chỉ được chứa file thường và thư mục; không chấp nhận symlink.")
		}
		filePath := filepath.Join(dest, filepath.FromSlash(name))
		if directory {
			if err = os.MkdirAll(filePath, 0700); err != nil {
				return total, uploadError(http.StatusBadRequest, "Cấu trúc ZIP bị xung đột.")
			}
			continue
		}
		if entry.UncompressedSize64 > uint64(max(0, limit-total)) {
			return total, uploadError(http.StatusRequestEntityTooLarge, "Website sau giải nén vượt hạn mức dung lượng.")
		}
		if err = os.MkdirAll(filepath.Dir(filePath), 0700); err != nil {
			return total, uploadError(http.StatusBadRequest, "Cấu trúc ZIP bị xung đột.")
		}
		input, err := entry.Open()
		if err != nil {
			return total, uploadError(http.StatusBadRequest, "Không thể đọc nội dung ZIP.")
		}
		output, err := os.OpenFile(filePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			input.Close()
			return total, uploadError(http.StatusBadRequest, "Cấu trúc ZIP bị xung đột.")
		}
		size, copyErr := budget.copy(output, input, limit-total)
		if copyErr == nil {
			copyErr = output.Sync()
		}
		input.Close()
		closeErr := output.Close()
		total += size
		if copyErr != nil {
			var failure *uploadFailure
			if errors.As(copyErr, &failure) {
				return total, copyErr
			}
			return total, uploadError(http.StatusBadRequest, "Nội dung ZIP bị hỏng hoặc kiểm tra checksum thất bại.")
		}
		if closeErr != nil {
			return total, closeErr
		}
	}
	return total, nil
}

func preflightZip(filename string, maxFiles int) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	tailSize := min(info.Size(), int64(65535+22))
	tail := make([]byte, tailSize)
	if _, err = f.ReadAt(tail, info.Size()-tailSize); err != nil {
		return uploadError(http.StatusBadRequest, "File ZIP bị hỏng.")
	}
	footer := -1
	for search := len(tail) - 22; search >= 0; search-- {
		if bytes.Equal(tail[search:search+4], []byte{'P', 'K', 5, 6}) && search+22+int(binary.LittleEndian.Uint16(tail[search+20:search+22])) == len(tail) {
			footer = search
			break
		}
	}
	if footer < 0 {
		return uploadError(http.StatusBadRequest, "File ZIP không hợp lệ.")
	}
	end := tail[footer:]
	count := int(binary.LittleEndian.Uint16(end[10:12]))
	if binary.LittleEndian.Uint16(end[4:6]) != 0 || binary.LittleEndian.Uint16(end[6:8]) != 0 || binary.LittleEndian.Uint16(end[8:10]) != uint16(count) {
		return uploadError(http.StatusBadRequest, "ZIP chia nhiều phần không được hỗ trợ.")
	}
	if count == 65535 || binary.LittleEndian.Uint32(end[12:16]) == 0xffffffff || binary.LittleEndian.Uint32(end[16:20]) == 0xffffffff {
		return uploadError(http.StatusBadRequest, "ZIP64 không được hỗ trợ; hãy tạo ZIP thông thường cho website nhỏ.")
	}
	if count > maxFiles {
		return uploadError(http.StatusRequestEntityTooLarge, "ZIP có quá nhiều file hoặc thư mục.")
	}
	centralSize := int64(binary.LittleEndian.Uint32(end[12:16]))
	if centralSize > 8*MiB {
		return uploadError(http.StatusRequestEntityTooLarge, "Metadata ZIP quá lớn. Hãy tạo lại ZIP với tên file và thông tin bổ sung gọn hơn.")
	}
	centralOffset := int64(binary.LittleEndian.Uint32(end[16:20]))
	footerOffset := info.Size() - tailSize + int64(footer)
	if centralOffset+centralSize != footerOffset {
		return uploadError(http.StatusBadRequest, "Cấu trúc ZIP không hợp lệ.")
	}
	reader := io.NewSectionReader(f, centralOffset, centralSize)
	var header [46]byte
	actualCount := 0
	for {
		_, err = io.ReadFull(reader, header[:])
		if err == io.EOF {
			break
		}
		if err != nil || !bytes.Equal(header[:4], []byte{'P', 'K', 1, 2}) {
			return uploadError(http.StatusBadRequest, "Danh mục ZIP bị hỏng.")
		}
		actualCount++
		if actualCount > maxFiles {
			return uploadError(http.StatusRequestEntityTooLarge, "ZIP có quá nhiều file hoặc thư mục.")
		}
		nameSize := int64(binary.LittleEndian.Uint16(header[28:30]))
		extraSize := int64(binary.LittleEndian.Uint16(header[30:32]))
		commentSize := int64(binary.LittleEndian.Uint16(header[32:34]))
		if nameSize > 1024 {
			return uploadError(http.StatusBadRequest, "Tên file trong ZIP quá dài.")
		}
		if _, err = io.CopyN(io.Discard, reader, nameSize+extraSize+commentSize); err != nil {
			return uploadError(http.StatusBadRequest, "Danh mục ZIP bị hỏng.")
		}
	}
	if actualCount != count {
		return uploadError(http.StatusBadRequest, "Số file trong ZIP không hợp lệ.")
	}
	return nil
}

func findWebRoot(content string) (string, error) {
	validIndex := func(dir string) bool {
		info, err := os.Lstat(filepath.Join(dir, "index.html"))
		return err == nil && info.Mode().IsRegular()
	}
	if validIndex(content) {
		return content, nil
	}
	entries, err := os.ReadDir(content)
	if err != nil {
		return "", err
	}
	if len(entries) == 1 && entries[0].IsDir() {
		wrapped := filepath.Join(content, entries[0].Name())
		if validIndex(wrapped) {
			return wrapped, nil
		}
	}
	return "", uploadError(http.StatusBadRequest, "Không tìm thấy index.html ở thư mục gốc. Hãy tải thư mục website đã build hoặc ZIP có một thư mục bọc ngoài.")
}

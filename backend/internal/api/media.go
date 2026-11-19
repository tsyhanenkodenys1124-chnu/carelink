package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"doglyad/backend/internal/security"
)

// Обмеження для фотографій: 5 МБ і лише растрові формати, які показує браузер.
const (
	maxPhotoBytes = 5 << 20
	photoField    = "photo"
)

var allowedPhotoTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// uploadWardPhoto зберігає фотографію підопічного для екрана пристрою.
func (s *Server) uploadWardPhoto(w http.ResponseWriter, r *http.Request) {
	wardID := r.PathValue("id")
	if !s.requireWard(w, r, wardID, permSettings) {
		return
	}
	// Попереднє фото прибираємо, щоб каталог медіа не ріс без потреби.
	var previous string
	if err := s.db.QueryRowContext(r.Context(),
		"SELECT COALESCE(photo_path,'') FROM wards WHERE id=?", wardID).Scan(&previous); err != nil {
		s.log.Warn("Не вдалося прочитати попереднє фото", "помилка", err)
	}
	mediaID, err := s.storePhoto(r, wardID, "")
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.db.ExecContext(r.Context(), "UPDATE wards SET photo_path=? WHERE id=?", mediaID, wardID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти фото.", err)
		return
	}
	if previous != "" && previous != mediaID {
		s.removeMediaFile(r.Context(), previous)
	}
	s.audit(r.Context(), wardID, helperOf(r).ID, "завантажено", "фото підопічного", mediaID)
	s.writeJSON(w, http.StatusCreated, map[string]string{"id": mediaID, "url": "/api/media/" + mediaID})
}

// uploadTaskPhoto зберігає фотографію ліків або предмета для завдання.
func (s *Server) uploadTaskPhoto(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	wardID, err := s.wardOfTask(r.Context(), taskID)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Завдання не знайдено.")
		return
	}
	if !s.requireWard(w, r, wardID, permTasks) {
		return
	}
	var previous string
	if err := s.db.QueryRowContext(r.Context(),
		"SELECT COALESCE(photo_media_id,'') FROM tasks WHERE id=?", taskID).Scan(&previous); err != nil {
		s.log.Warn("Не вдалося прочитати попереднє фото", "помилка", err)
	}
	mediaID, err := s.storePhoto(r, wardID, taskID)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.db.ExecContext(r.Context(), "UPDATE tasks SET photo_media_id=? WHERE id=?", mediaID, taskID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти фото.", err)
		return
	}
	if previous != "" && previous != mediaID {
		s.removeMediaFile(r.Context(), previous)
	}
	s.audit(r.Context(), wardID, helperOf(r).ID, "завантажено", "фото завдання", mediaID)
	s.writeJSON(w, http.StatusCreated, map[string]string{"id": mediaID, "url": "/api/media/" + mediaID})
}

// storePhoto читає файл із форми, перевіряє тип і кладе його в каталог медіа.
func (s *Server) storePhoto(r *http.Request, wardID, taskID string) (string, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxPhotoBytes+(1<<16))
	if err := r.ParseMultipartForm(maxPhotoBytes); err != nil {
		return "", errInvalid("Файл завеликий або пошкоджений. Максимум 5 МБ.")
	}
	file, header, err := r.FormFile(photoField)
	if err != nil {
		return "", errInvalid("Додайте файл у полі «photo».")
	}
	defer file.Close()
	if header.Size > maxPhotoBytes {
		return "", errInvalid("Фото має бути не більшим за 5 МБ.")
	}

	// Тип визначаємо за вмістом, а не за назвою файлу.
	head := make([]byte, 512)
	read, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", errInvalid("Не вдалося прочитати файл.")
	}
	head = head[:read]
	mimeType := http.DetectContentType(head)
	extension, ok := allowedPhotoTypes[mimeType]
	if !ok {
		return "", errInvalid("Підтримуються лише зображення JPEG, PNG або WebP.")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", errInvalid("Не вдалося прочитати файл.")
	}

	mediaID := security.ID()
	name := mediaID + extension
	path := filepath.Join(s.cfg.MediaDir, name)
	target, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		s.logError("Не вдалося створити файл медіа", err)
		return "", errInvalid("Не вдалося зберегти файл.")
	}
	written, err := io.Copy(target, io.LimitReader(file, maxPhotoBytes))
	closeErr := target.Close()
	if err != nil || closeErr != nil {
		os.Remove(path)
		s.logError("Не вдалося записати файл медіа", errors.Join(err, closeErr))
		return "", errInvalid("Не вдалося зберегти файл.")
	}

	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO media(id,ward_id,task_id,path,mime_type,byte_size,created_by,created_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		mediaID, wardID, nullIfEmpty(taskID), name, mimeType, written, helperOf(r).ID, s.nowStamp()); err != nil {
		os.Remove(path)
		s.logError("Не вдалося зберегти запис медіа", err)
		return "", errInvalid("Не вдалося зберегти файл.")
	}
	return mediaID, nil
}

// serveMedia віддає фото помічникові або пристрою того самого підопічного.
func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request) {
	mediaID := r.PathValue("id")
	var wardID, name, mimeType string
	err := s.db.QueryRowContext(r.Context(),
		"SELECT ward_id,path,mime_type FROM media WHERE id=?", mediaID).Scan(&wardID, &name, &mimeType)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Файл не знайдено.")
		return
	}
	if !s.mayReadWard(r, wardID) {
		s.writeError(w, http.StatusForbidden, "Немає доступу до цього файлу.")
		return
	}
	// Ім'я формує сервер, але шлях усе одно перевіряємо перед відкриттям.
	if strings.ContainsAny(name, `/\`) {
		s.writeError(w, http.StatusNotFound, "Файл не знайдено.")
		return
	}
	path := filepath.Join(s.cfg.MediaDir, filepath.Base(name))
	file, err := os.Open(path)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Файл не знайдено.")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати файл.", err)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, name, info.ModTime(), file)
}

// mayReadWard перевіряє доступ помічника за сесією або пристрою за токеном.
func (s *Server) mayReadWard(r *http.Request, wardID string) bool {
	if token := r.Header.Get("X-Device-Token"); token != "" {
		var deviceWard string
		err := s.db.QueryRowContext(r.Context(),
			"SELECT ward_id FROM devices WHERE token_hash=? AND revoked_at IS NULL",
			security.Hash(token)).Scan(&deviceWard)
		if err == nil && deviceWard == wardID {
			return true
		}
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	var helperID string
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT h.id FROM sessions s JOIN helpers h ON h.id=s.helper_id
		WHERE s.token_hash=? AND s.expires_at>? AND s.revoked_at IS NULL AND h.deleted_at IS NULL`,
		security.Hash(cookie.Value), s.nowStamp()).Scan(&helperID); err != nil {
		return false
	}
	rights, err := s.rightsFor(r.Context(), helperID, wardID)
	return err == nil && rights.View
}

// removeMediaFile прибирає файл із диска та запис із бази.
func (s *Server) removeMediaFile(ctx context.Context, mediaID string) {
	var name string
	if err := s.db.QueryRowContext(ctx, "SELECT path FROM media WHERE id=?", mediaID).Scan(&name); err != nil {
		return
	}
	if err := os.Remove(filepath.Join(s.cfg.MediaDir, filepath.Base(name))); err != nil && !os.IsNotExist(err) {
		s.log.Warn("Не вдалося видалити файл медіа", "помилка", err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM media WHERE id=?", mediaID); err != nil {
		s.log.Warn("Не вдалося видалити запис медіа", "помилка", err)
	}
}

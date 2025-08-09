package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// TODO: implement the upload here
	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	// "thumbnail" should match the HTML form input name
	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	// `file` is an `io.Reader` that we can read from to get the image data

	content_type := header.Header.Get("Content-Type")

	video_metadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to retrieve video metadata", err)
		return
	}

	if userID != video_metadata.UserID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized user", err)
		return
	}

	extensions, err := mime.ExtensionsByType(content_type)
	var extension string
	if len(extensions) > 0 {
		extension = extensions[0]
	} else {
		respondWithError(w, http.StatusBadRequest, "Filetype error", err)
		return
	}
	new_file_path := filepath.Join(cfg.assetsRoot, videoIDString+extension)

	new_file, err := os.Create(new_file_path)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Creation error", err)
		return
	}

	media_type, _, err := mime.ParseMediaType(content_type)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error parsing media type", err)
		return
	}
	if media_type != "image/jpeg" && media_type != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Incorrect file type", nil)
		return
	}

	if _, err := io.Copy(new_file, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Copy error", err)
		return
	}
	defer new_file.Close()

	s := fmt.Sprintf("http://localhost:%s/assets/%s%s", cfg.port, videoIDString, extension)
	video_metadata.ThumbnailURL = &s

	err = cfg.db.UpdateVideo(video_metadata)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to update thumbnail URL", err)
		return
	}
	respondWithJSON(w, http.StatusOK, video_metadata)
}

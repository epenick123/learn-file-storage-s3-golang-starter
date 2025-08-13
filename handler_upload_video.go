package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)
	videoID := r.PathValue("videoID")
	video_uuid, err := uuid.Parse(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Error parsing video ID", err)
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

	video_metadata, err := cfg.db.GetVideo(video_uuid)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to retrieve video metadata", err)
		return
	}

	if userID != video_metadata.UserID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized user", nil)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to parse file", err)
		return
	}
	defer file.Close()
	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil || mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Invalid file type", err)
		return
	}

	temp_file, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating temp file", err)
		return
	}
	defer os.Remove(temp_file.Name())
	defer temp_file.Close()

	_, err = io.Copy(temp_file, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error copying file", err)
		return
	}

	temp_file.Seek(0, io.SeekStart)

	extensions, err := mime.ExtensionsByType(mediaType)
	var extension string
	if len(extensions) > 0 {
		extension = ".mp4"
	} else {
		respondWithError(w, http.StatusBadRequest, "Filetype error", err)
		return
	}
	// Generate random bytes
	new_key := make([]byte, 32)
	_, err = rand.Read(new_key)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Creation error", err)
		return
	}

	// Encode to base64 string
	hex_key := hex.EncodeToString(new_key)

	// Create filepath
	aspect_ratio, err := getVideoAspectRatio(temp_file.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error determining aspect ratio", err)
		return
	}

	key := aspect_ratio + "/" + hex_key + extension

	processed_file_path, err := processVideoForFastStart(temp_file.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error processing video for fast start", err)
		return
	}

	processedFile, err := os.Open(processed_file_path)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error opening file at file path", err)
		return
	}

	defer os.Remove(processed_file_path)
	defer processedFile.Close()

	input := s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &key,
		Body:        processedFile,
		ContentType: &mediaType,
	}

	_, err = cfg.s3Client.PutObject(context.Background(), &input)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error uploading to S3", err)
		return
	}

	// Create the S3 URL
	s3URL := "https://" + cfg.s3Bucket + ".s3." + cfg.s3Region + ".amazonaws.com/" + key

	// Update the video metadata
	video_metadata.VideoURL = &s3URL
	err = cfg.db.UpdateVideo(video_metadata)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error updating video in database", err)
		return
	}

	// Return success response
	respondWithJSON(w, http.StatusOK, video_metadata)
}

func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	buffer := &bytes.Buffer{}
	cmd.Stdout = buffer
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	type Stream struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}

	type ProbeOutput struct {
		Streams []Stream `json:"streams"`
	}

	var result ProbeOutput
	err = json.Unmarshal(buffer.Bytes(), &result)
	if err != nil {
		return "", err
	}

	if len(result.Streams) == 0 {
		return "", errors.New("no streams found")
	}

	width := result.Streams[0].Width
	height := result.Streams[0].Height
	ratio := float64(width) / float64(height)
	if math.Abs(ratio-16.0/9.0) < 0.05 {
		return "landscape", nil
	} else if math.Abs(ratio-9.0/16.0) < 0.05 {
		return "portrait", nil
	}
	return "other", nil

}

func processVideoForFastStart(filePath string) (string, error) {
	extension := filepath.Ext(filePath)
	name := filePath[0 : len(filePath)-len(extension)]
	output_path := name + ".processing" + extension

	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", output_path)
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	return output_path, nil
}

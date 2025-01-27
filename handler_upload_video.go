package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	uploadLimit := 1 << 30
	http.MaxBytesReader(w, r.Body, int64(uploadLimit))

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

	videoMetadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video with given ID not found", err)
		return
	}

	if videoMetadata.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Not the video owner", nil)
		return
	}

	videoFile, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error parsing multipart form", err)
		return
	}
	defer videoFile.Close()

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if mediaType != "video/mp4" || err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not verify filetype as mp4", err)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "An error occurred", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	io.Copy(tempFile, videoFile)
	tempFile.Seek(0, io.SeekStart)

	aspectRatio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "An error occurred", err)
		return
	}

	randomID := make([]byte, 32, 32)
	rand.Read(randomID)
	randomIDString := base64.RawURLEncoding.EncodeToString(randomID)
	key := fmt.Sprintf("%s/%s.mp4", aspectRatio, randomIDString)

	fastStartFileName, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "An error occurred", err)
		return
	}

	fastStartFile, err := os.Open(fastStartFileName)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "An error occurred", err)
		return
	}
	defer os.Remove(fastStartFile.Name())
	defer fastStartFile.Close()

	params := s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &key,
		Body:        fastStartFile,
		ContentType: &mediaType,
	}

	_, err = cfg.s3Client.PutObject(r.Context(), &params)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not put object to S3", err)
	}

	videoURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, key)

	updatedVid := database.Video{
		ID:                videoMetadata.ID,
		CreatedAt:         videoMetadata.CreatedAt,
		UpdatedAt:         videoMetadata.UpdatedAt,
		ThumbnailURL:      videoMetadata.ThumbnailURL,
		VideoURL:          &videoURL,
		CreateVideoParams: videoMetadata.CreateVideoParams,
	}

	cfg.db.UpdateVideo(updatedVid)
}

func getVideoAspectRatio(filepath string) (string, error) {
	var output bytes.Buffer

	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filepath)
	cmd.Stdout = &output

	err := cmd.Run()
	if err != nil {
		return "", err
	}

	var data struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}

	if err := json.Unmarshal(output.Bytes(), &data); err != nil {
		return "", err
	}

	if len(data.Streams) == 0 {
		return "", nil
	}

	aspectRatio := float64(data.Streams[0].Width) / float64(data.Streams[0].Height)
	tolerance := 0.1
	landscapeRatio := float64(16) / float64(9)
	portraitRatio := float64(9) / float64(16)

	if math.Abs(aspectRatio-landscapeRatio) < tolerance {
		return "landscape", nil
	} else if math.Abs(aspectRatio-portraitRatio) < tolerance {
		return "portrait", nil
	}

	return "other", nil
}

func processVideoForFastStart(filePath string) (string, error) {
	outputFilePath := fmt.Sprintf("%s.processing", filePath)

	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outputFilePath)

	err := cmd.Run()
	if err != nil {
		return "", err
	}

	return outputFilePath, nil
}

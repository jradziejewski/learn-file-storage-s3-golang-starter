package main

import (
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
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

	maxMemory := int64(10 << 20)
	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error parsing multipart form", err)
		return
	}

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error reading thumbnail", err)
		return
	}
	defer file.Close()

	extension, err := getExtensionFromHeader(header)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Wrong header", err)
		return
	}
	filename := fmt.Sprintf("%s.%s", videoID, extension)

	videoMetadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video with given ID not found", err)
		return
	}

	if videoMetadata.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Not the video owner", nil)
		return
	}

	filePath := filepath.Join(cfg.assetsRoot, filename)

	createdFile, err := os.Create(filePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating file", err)
		return
	}
	defer createdFile.Close()
	_, err = io.Copy(createdFile, io.Reader(file))
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating file", err)
		return
	}

	thumbnailUrl := fmt.Sprintf("/assets/%s", filename)

	video := database.Video{
		ID:                videoID,
		CreatedAt:         videoMetadata.CreatedAt,
		UpdatedAt:         videoMetadata.UpdatedAt,
		ThumbnailURL:      &thumbnailUrl,
		VideoURL:          videoMetadata.VideoURL,
		CreateVideoParams: videoMetadata.CreateVideoParams,
	}

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error updating video in db", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}

func getExtensionFromHeader(header *multipart.FileHeader) (string, error) {
	contentHeader := header.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentHeader)
	if (mediaType != "image/jpeg" && mediaType != "image/png") || err != nil {
		return "", fmt.Errorf("Wrong media type")
	}
	splitType := strings.SplitN(mediaType, "/", 2)

	return splitType[1], nil
}

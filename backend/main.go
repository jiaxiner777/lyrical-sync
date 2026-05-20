package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"gorm.io/gorm"
	"lyrical-sync-backend/database"
	"lyrical-sync-backend/services"
)

type parseSongRequest struct {
	Lyrics string `json:"lyrics"`
}

type adminAddSongRequest struct {
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	RawLyrics string `json:"raw_lyrics"`
}

type loadSongRequest struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
}

type songSearchItem struct {
	ID     uint   `json:"id"`
	Title  string `json:"title"`
	Artist string `json:"artist"`
}

type songDetailResponse struct {
	SongID string              `json:"songId"`
	Title  string              `json:"title"`
	Artist string              `json:"artist"`
	Lines  []services.SongLine `json:"lines"`
}

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}

		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization")
		c.Header("Access-Control-Expose-Headers", "Content-Length, Content-Type")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

func main() {
	if err := godotenv.Load(".env"); err != nil {
		if fallbackErr := godotenv.Load("backend/.env"); fallbackErr != nil {
			log.Printf("warning: could not load .env: %v", err)
		}
	}
	if err := database.InitDB(); err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}

	r := gin.Default()
	r.Use(CORSMiddleware())

	r.POST("/api/song/parse", parseSongHandler)
	r.POST("/api/songs/admin/add", adminAddSongHandler)
	r.POST("/api/songs/load", loadSongHandler)
	r.GET("/api/songs/search", searchSongsHandler)
	r.GET("/api/songs/:id", getSongDetailHandler)

	r.Run(":8080")
}

func parseSongHandler(c *gin.Context) {
	var request parseSongRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":     "invalid request body",
			"code":      services.ServiceErrorInvalidInput,
			"retryable": false,
		})
		return
	}

	lyrics := strings.TrimSpace(request.Lyrics)
	if lyrics == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":     "lyrics is required",
			"code":      services.ServiceErrorInvalidInput,
			"retryable": false,
		})
		return
	}

	result, err := services.GenerateFullSongJSON(c.Request.Context(), lyrics)
	if err != nil {
		statusCode, responseBody := mapServiceError(err)
		c.JSON(statusCode, responseBody)
		return
	}

	c.JSON(http.StatusOK, result)
}

func adminAddSongHandler(c *gin.Context) {
	var request adminAddSongRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	title := strings.TrimSpace(request.Title)
	artist := strings.TrimSpace(request.Artist)
	rawLyrics := strings.TrimSpace(request.RawLyrics)
	if title == "" || artist == "" || rawLyrics == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title, artist and raw_lyrics are required"})
		return
	}

	parsedSong, err := services.GenerateFullSongJSON(c.Request.Context(), rawLyrics)
	if err != nil {
		statusCode, responseBody := mapServiceError(err)
		c.JSON(statusCode, responseBody)
		return
	}

	syllableData, err := json.Marshal(parsedSong)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to serialize syllable data"})
		return
	}

	song := database.Song{
		Title:        title,
		Artist:       artist,
		RawLyrics:    rawLyrics,
		SyllableData: string(syllableData),
	}
	if err := database.GlobalDB.Create(&song).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save song to database"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":     song.ID,
		"title":  song.Title,
		"artist": song.Artist,
	})
}

func loadSongHandler(c *gin.Context) {
	var request loadSongRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":     "invalid request body",
			"code":      services.ServiceErrorInvalidInput,
			"retryable": false,
		})
		return
	}

	title := strings.TrimSpace(request.Title)
	artist := strings.TrimSpace(request.Artist)
	if title == "" || artist == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":     "title and artist are required",
			"code":      services.ServiceErrorInvalidInput,
			"retryable": false,
		})
		return
	}

	song, found, err := findSongByIdentity(title, artist)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query local song cache"})
		return
	}

	if found && strings.TrimSpace(song.SyllableData) != "" {
		cachedResponse, parseErr := buildSongDetailResponse(song)
		if parseErr == nil {
			c.JSON(http.StatusOK, cachedResponse)
			return
		}
	}

	rawLyrics := ""
	if found {
		rawLyrics = strings.TrimSpace(song.RawLyrics)
	}
	if rawLyrics == "" {
		fetchedLyrics, fetchErr := services.FetchLyrics(c.Request.Context(), title, artist)
		if fetchErr != nil {
			statusCode, responseBody := mapServiceError(fetchErr)
			c.JSON(statusCode, responseBody)
			return
		}
		rawLyrics = fetchedLyrics
	}

	parsedSong, parseErr := services.GenerateFullSongJSON(c.Request.Context(), rawLyrics)
	if parseErr != nil {
		statusCode, responseBody := mapServiceError(parseErr)
		c.JSON(statusCode, responseBody)
		return
	}

	syllableData, err := json.Marshal(parsedSong)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to serialize syllable data"})
		return
	}

	if found {
		song.Title = title
		song.Artist = artist
		song.RawLyrics = rawLyrics
		song.SyllableData = string(syllableData)
		if err := database.GlobalDB.Save(&song).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update song cache"})
			return
		}
	} else {
		song = database.Song{
			Title:        title,
			Artist:       artist,
			RawLyrics:    rawLyrics,
			SyllableData: string(syllableData),
		}
		if err := database.GlobalDB.Create(&song).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save song to database"})
			return
		}
	}

	c.JSON(http.StatusOK, songDetailResponse{
		SongID: parsedSong.SongID,
		Title:  song.Title,
		Artist: song.Artist,
		Lines:  parsedSong.Lines,
	})
}

func searchSongsHandler(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("keyword"))
	title := strings.TrimSpace(c.Query("title"))
	artist := strings.TrimSpace(c.Query("artist"))

	limit := 20
	if keyword == "" && title == "" && artist == "" {
		limit = 8
	}

	var songs []songSearchItem
	query := database.GlobalDB.
		Model(&database.Song{}).
		Select("id", "title", "artist").
		Order("id DESC").
		Limit(limit)

	if keyword != "" {
		pattern := "%" + keyword + "%"
		query = query.Where("title LIKE ? OR artist LIKE ?", pattern, pattern)
	} else {
		if title != "" {
			query = query.Where("title LIKE ?", "%"+title+"%")
		}
		if artist != "" {
			query = query.Where("artist LIKE ?", "%"+artist+"%")
		}
	}

	if err := query.Find(&songs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to search songs"})
		return
	}

	c.JSON(http.StatusOK, songs)
}

func getSongDetailHandler(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid song id"})
		return
	}

	var song database.Song
	if err := database.GlobalDB.First(&song, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "song not found"})
		return
	}

	response, err := buildSongDetailResponse(song)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decode stored syllable data"})
		return
	}

	c.JSON(http.StatusOK, response)
}

func findSongByIdentity(title string, artist string) (database.Song, bool, error) {
	var song database.Song
	result := database.GlobalDB.
		Where("LOWER(title) = LOWER(?) AND LOWER(artist) = LOWER(?)", title, artist).
		Order("id DESC").
		First(&song)
	if result.Error == nil {
		return song, true, nil
	}
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return database.Song{}, false, nil
	}
	return database.Song{}, false, result.Error
}

func buildSongDetailResponse(song database.Song) (songDetailResponse, error) {
	var parsed services.SongSyllableData
	if err := json.Unmarshal([]byte(song.SyllableData), &parsed); err != nil {
		return songDetailResponse{}, err
	}

	return songDetailResponse{
		SongID: parsed.SongID,
		Title:  song.Title,
		Artist: song.Artist,
		Lines:  parsed.Lines,
	}, nil
}

func mapServiceError(err error) (int, gin.H) {
	var serviceErr *services.ServiceError
	if !errors.As(err, &serviceErr) {
		return http.StatusInternalServerError, gin.H{
			"error":     "internal server error",
			"code":      services.ServiceErrorInternal,
			"retryable": false,
		}
	}

	statusCode := http.StatusInternalServerError
	switch serviceErr.Kind {
	case services.ServiceErrorInvalidInput:
		statusCode = http.StatusBadRequest
	case services.ServiceErrorTooLarge:
		statusCode = http.StatusUnprocessableEntity
	case services.ServiceErrorRateLimited:
		statusCode = http.StatusTooManyRequests
	case services.ServiceErrorUpstreamTimeout:
		statusCode = http.StatusGatewayTimeout
	case services.ServiceErrorUpstreamUnavailable:
		statusCode = http.StatusServiceUnavailable
	case services.ServiceErrorUpstreamBadResp:
		statusCode = http.StatusBadGateway
	}

	return statusCode, gin.H{
		"error":     serviceErr.Message,
		"code":      serviceErr.Kind,
		"retryable": serviceErr.Retryable,
	}
}

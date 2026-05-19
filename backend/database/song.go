package database

type Song struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	Title        string `gorm:"index" json:"title"`
	Artist       string `gorm:"index" json:"artist"`
	RawLyrics    string `json:"raw_lyrics"`
	SyllableData string `gorm:"type:text" json:"syllable_data"`
}

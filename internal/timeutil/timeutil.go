package timeutil

import "time"

// JakartaLocation mengembalikan lokasi zona waktu Asia/Jakarta (WIB, UTC+7).
// Jika tzdata sistem tidak tersedia, fallback ke fixed zone UTC+7.
func JakartaLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		loc = time.FixedZone("Asia/Jakarta", 7*3600)
	}
	return loc
}

// GetJakartaStartOfDay mengembalikan awal hari (00:00:00) zona waktu Asia/Jakarta untuk saat ini.
func GetJakartaStartOfDay() time.Time {
	loc := JakartaLocation()
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
}

// StartOfDayJakarta mengembalikan awal hari (00:00:00) zona waktu Asia/Jakarta untuk waktu t tertentu.
func StartOfDayJakarta(t time.Time) time.Time {
	loc := JakartaLocation()
	inLoc := t.In(loc)
	return time.Date(inLoc.Year(), inLoc.Month(), inLoc.Day(), 0, 0, 0, 0, loc)
}

// GetNextJakartaMidnight mengembalikan timestamp tengah malam berikutnya (00:00:00 besok) zona Asia/Jakarta.
func GetNextJakartaMidnight() time.Time {
	startToday := GetJakartaStartOfDay()
	return startToday.AddDate(0, 0, 1)
}

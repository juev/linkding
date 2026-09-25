package httpserver

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

var englishBookmarkMonths = [...]string{"Jan.", "Feb.", "March", "April", "May", "June", "July", "Aug.", "Sept.", "Oct.", "Nov.", "Dec."}
var russianBookmarkMonths = [...]string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"}

func formatBookmarkDetailsDate(value time.Time, r *http.Request, zone string) string {
	location, err := time.LoadLocation(zone)
	if err != nil {
		location = time.UTC
	}
	value = value.In(location)
	language := r.Header.Get("Accept-Language")
	if cookie, err := r.Cookie("ld_language"); err == nil {
		language = cookie.Value
	}
	language = strings.ToLower(strings.TrimSpace(strings.Split(language, ",")[0]))
	if strings.HasPrefix(language, "ru") {
		return fmt.Sprintf("%d %s %d г. %02d:%02d", value.Day(), russianBookmarkMonths[int(value.Month())-1], value.Year(), value.Hour(), value.Minute())
	}
	hour := value.Hour() % 12
	if hour == 0 {
		hour = 12
	}
	period := "a.m."
	if value.Hour() >= 12 {
		period = "p.m."
	}
	return fmt.Sprintf("%s %d, %d, %d:%02d %s", englishBookmarkMonths[int(value.Month())-1], value.Day(), value.Year(), hour, value.Minute(), period)
}

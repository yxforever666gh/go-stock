package data

import (
	"sync"
	"time"
)

var (
	cnLocOnce sync.Once
	cnLoc     *time.Location
)

// cnLocation returns the canonical location used by CN A-share trading time.
// Prefer Asia/Shanghai but fall back to a fixed +08:00 zone when tzdata isn't
// available in the runtime.
func cnLocation() *time.Location {
	cnLocOnce.Do(func() {
		loc, err := time.LoadLocation("Asia/Shanghai")
		if err != nil || loc == nil {
			cnLoc = time.FixedZone("CST", 8*3600)
			return
		}
		cnLoc = loc
	})
	if cnLoc == nil {
		return time.FixedZone("CST", 8*3600)
	}
	return cnLoc
}


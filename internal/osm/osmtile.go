package osm

import (
	"fmt"
	"math"
)

func GetOSMTileURL(lat float64, lon float64, zoom int) string {
	return fmt.Sprintf("https://tile.openstreetmap.org/%d/%.0f/%.0f.png", zoom, math.Floor((lon+180)/360*math.Pow(2, float64(zoom))), math.Floor((1-math.Log(math.Tan(lat*math.Pi/180)+1/math.Cos(lat*math.Pi/180))/math.Pi)/2*math.Pow(2, float64(zoom))))
}

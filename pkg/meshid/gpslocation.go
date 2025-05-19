package meshid

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type GeoURI struct {
	Latitude  float32
	Longitude float32
	Altitude  *float32

	// Standard parameters
	Uncertainty *float32 // `u=`
	CRS         string   // `crs=`

	// Android-specific
	Zoom  *int   // z=
	Query string // q=

	OtherParams map[string]string
	QueryParams map[string]string // Additional ?params
}

func ParseGeoURI(uri string) (*GeoURI, error) {
	if !strings.HasPrefix(uri, "geo:") {
		return nil, errors.New("invalid geo URI: must start with 'geo:'")
	}

	raw := strings.TrimPrefix(uri, "geo:")

	var mainPart, rawQuery string
	if i := strings.Index(raw, "?"); i != -1 {
		mainPart = raw[:i]
		rawQuery = raw[i+1:]
	} else {
		mainPart = raw
	}

	parts := strings.Split(mainPart, ";")
	coordTokens := strings.Split(parts[0], ",")

	if len(coordTokens) < 2 || len(coordTokens) > 3 {
		return nil, errors.New("expected 2 or 3 coordinates")
	}

	lat64, err := strconv.ParseFloat(coordTokens[0], 32)
	if err != nil {
		return nil, fmt.Errorf("invalid latitude: %v", err)
	}
	lon64, err := strconv.ParseFloat(coordTokens[1], 32)
	if err != nil {
		return nil, fmt.Errorf("invalid longitude: %v", err)
	}

	var alt *float32
	if len(coordTokens) == 3 {
		a64, err := strconv.ParseFloat(coordTokens[2], 32)
		if err != nil {
			return nil, fmt.Errorf("invalid altitude: %v", err)
		}
		a := float32(a64)
		alt = &a
	}

	geo := &GeoURI{
		Latitude:    float32(lat64),
		Longitude:   float32(lon64),
		Altitude:    alt,
		OtherParams: make(map[string]string),
		QueryParams: make(map[string]string),
	}

	// Parse ; parameters
	for _, p := range parts[1:] {
		if p == "" {
			continue
		}
		kv := strings.SplitN(p, "=", 2)
		key, _ := url.QueryUnescape(kv[0])
		val := ""
		if len(kv) == 2 {
			val, _ = url.QueryUnescape(kv[1])
		}

		switch key {
		case "u":
			u64, err := strconv.ParseFloat(val, 32)
			if err != nil {
				return nil, fmt.Errorf("invalid u= uncertainty: %v", err)
			}
			u := float32(u64)
			geo.Uncertainty = &u
		case "crs":
			geo.CRS = val
		default:
			geo.OtherParams[key] = val
		}
	}

	// Parse query parameters like ?z= and ?q=
	if rawQuery != "" {
		values, err := url.ParseQuery(rawQuery)
		if err != nil {
			return nil, fmt.Errorf("invalid query string: %v", err)
		}
		for key, val := range values {
			switch key {
			case "z":
				z, err := strconv.Atoi(val[0])
				if err != nil {
					return nil, fmt.Errorf("invalid zoom level: %v", err)
				}
				geo.Zoom = &z
			case "q":
				geo.Query = val[0]
			default:
				geo.QueryParams[key] = val[0]
			}
		}
	}

	return geo, nil
}

func (g *GeoURI) String() string {
	builder := strings.Builder{}
	builder.WriteString(fmt.Sprintf("geo:%.5f,%.5f", g.Latitude, g.Longitude))
	if g.Altitude != nil {
		builder.WriteString(fmt.Sprintf(",%.3f", *g.Altitude))
	}

	params := []string{}
	if g.Uncertainty != nil {
		params = append(params, "u="+strconv.FormatFloat(float64(*g.Uncertainty), 'f', -1, 32))
	}
	if g.CRS != "" {
		params = append(params, "crs="+url.QueryEscape(g.CRS))
	}
	for k, v := range g.OtherParams {
		params = append(params, url.QueryEscape(k)+"="+url.QueryEscape(v))
	}
	sort.Strings(params)
	for _, p := range params {
		builder.WriteString(";" + p)
	}

	query := url.Values{}
	if g.Zoom != nil {
		query.Set("z", strconv.Itoa(*g.Zoom))
	}
	if g.Query != "" {
		query.Set("q", g.Query)
	}
	for k, v := range g.QueryParams {
		query.Set(k, v)
	}
	if encoded := query.Encode(); encoded != "" {
		builder.WriteString("?" + encoded)
	}

	return builder.String()
}

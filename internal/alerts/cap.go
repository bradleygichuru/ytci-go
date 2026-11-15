package alerts

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"
)

// CAPAlert is the normalized CAP 1.2 alert shape used by the ingester.
type CAPAlert struct {
	Identifier string
	Sender     string
	Sent       time.Time
	Status     string
	MsgType    string
	Scope      string
	References string
	Infos      []CAPInfo
}

type CAPInfo struct {
	Language    string
	Categories  []string
	Event       string
	Urgency     string
	Severity    string
	Certainty   string
	Effective   *time.Time
	Onset       *time.Time
	Expires     *time.Time
	Headline    string
	Description string
	Instruction string
	Web         string
	Contact     string
	Areas       []CAPArea
}

type CAPArea struct {
	AreaDesc string
	Polygons []string
	Circles  []string
	Geocodes []CAPGeocode
}

type CAPGeocode struct {
	ValueName string
	Value     string
}

type CAPFeed struct {
	LastBuildDate time.Time
	Items         []CAPItem
}

type CAPItem struct {
	Title       string
	Link        string
	Description string
	PubDate     time.Time
	GUID        string
}

type capAlertXML struct {
	XMLName    xml.Name     `xml:"alert"`
	Identifier string       `xml:"identifier"`
	Sender     string       `xml:"sender"`
	Sent       string       `xml:"sent"`
	Status     string       `xml:"status"`
	MsgType    string       `xml:"msgType"`
	Scope      string       `xml:"scope"`
	References string       `xml:"references"`
	Infos      []capInfoXML `xml:"info"`
}

type capInfoXML struct {
	Language    string       `xml:"language"`
	Categories  []string     `xml:"category"`
	Event       string       `xml:"event"`
	Urgency     string       `xml:"urgency"`
	Severity    string       `xml:"severity"`
	Certainty   string       `xml:"certainty"`
	Effective   string       `xml:"effective"`
	Onset       string       `xml:"onset"`
	Expires     string       `xml:"expires"`
	Headline    string       `xml:"headline"`
	Description string       `xml:"description"`
	Instruction string       `xml:"instruction"`
	Web         string       `xml:"web"`
	Contact     string       `xml:"contact"`
	Areas       []capAreaXML `xml:"area"`
}

type capAreaXML struct {
	AreaDesc string          `xml:"areaDesc"`
	Polygons []string        `xml:"polygon"`
	Circles  []string        `xml:"circle"`
	Geocodes []capGeocodeXML `xml:"geocode"`
}

type capGeocodeXML struct {
	ValueName string `xml:"valueName"`
	Value     string `xml:"value"`
}

type rssXML struct {
	Channel rssChannelXML `xml:"channel"`
}

type rssChannelXML struct {
	LastBuildDate string       `xml:"lastBuildDate"`
	Items         []rssItemXML `xml:"item"`
}

type rssItemXML struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	GUID        string `xml:"guid"`
}

// ParseCAP parses one CAP 1.2 XML document. It accepts namespaced CAP roots.
func ParseCAP(r io.Reader) (*CAPAlert, error) {
	var doc capAlertXML
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse CAP XML: %w", err)
	}
	sent, err := parseCAPTime(doc.Sent)
	if err != nil {
		return nil, fmt.Errorf("CAP sent: %w", err)
	}
	alert := &CAPAlert{
		Identifier: strings.TrimSpace(doc.Identifier),
		Sender:     strings.TrimSpace(doc.Sender),
		Sent:       sent,
		Status:     strings.TrimSpace(doc.Status),
		MsgType:    strings.TrimSpace(doc.MsgType),
		Scope:      strings.TrimSpace(doc.Scope),
		References: strings.TrimSpace(doc.References),
	}
	for _, infoXML := range doc.Infos {
		info := CAPInfo{
			Language:    strings.TrimSpace(infoXML.Language),
			Categories:  cleanStrings(infoXML.Categories),
			Event:       strings.TrimSpace(infoXML.Event),
			Urgency:     strings.TrimSpace(infoXML.Urgency),
			Severity:    strings.TrimSpace(infoXML.Severity),
			Certainty:   strings.TrimSpace(infoXML.Certainty),
			Headline:    strings.TrimSpace(infoXML.Headline),
			Description: strings.TrimSpace(infoXML.Description),
			Instruction: strings.TrimSpace(infoXML.Instruction),
			Web:         strings.TrimSpace(infoXML.Web),
			Contact:     strings.TrimSpace(infoXML.Contact),
		}
		info.Effective = parseCAPTimePtr(infoXML.Effective)
		info.Onset = parseCAPTimePtr(infoXML.Onset)
		info.Expires = parseCAPTimePtr(infoXML.Expires)
		for _, areaXML := range infoXML.Areas {
			area := CAPArea{
				AreaDesc: strings.TrimSpace(areaXML.AreaDesc),
				Polygons: cleanStrings(areaXML.Polygons),
				Circles:  cleanStrings(areaXML.Circles),
			}
			for _, gc := range areaXML.Geocodes {
				area.Geocodes = append(area.Geocodes, CAPGeocode{
					ValueName: strings.TrimSpace(gc.ValueName),
					Value:     strings.TrimSpace(gc.Value),
				})
			}
			info.Areas = append(info.Areas, area)
		}
		alert.Infos = append(alert.Infos, info)
	}
	return alert, nil
}

// ParseCAPRSS parses the KMD CAP RSS index.
func ParseCAPRSS(r io.Reader) (*CAPFeed, error) {
	var doc rssXML
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse CAP RSS: %w", err)
	}
	feed := &CAPFeed{}
	if doc.Channel.LastBuildDate != "" {
		if t, err := time.Parse(time.RFC1123Z, strings.TrimSpace(doc.Channel.LastBuildDate)); err == nil {
			feed.LastBuildDate = t
		}
	}
	for _, itemXML := range doc.Channel.Items {
		item := CAPItem{
			Title:       strings.TrimSpace(itemXML.Title),
			Link:        strings.TrimSpace(itemXML.Link),
			Description: strings.TrimSpace(itemXML.Description),
			GUID:        strings.TrimSpace(itemXML.GUID),
		}
		if itemXML.PubDate != "" {
			if t, err := time.Parse(time.RFC1123Z, strings.TrimSpace(itemXML.PubDate)); err == nil {
				item.PubDate = t
			}
		}
		feed.Items = append(feed.Items, item)
	}
	return feed, nil
}

func parseCAPTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

func parseCAPTimePtr(value string) *time.Time {
	t, err := parseCAPTime(value)
	if err != nil {
		return nil
	}
	return &t
}

func cleanStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

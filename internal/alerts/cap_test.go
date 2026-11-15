package alerts

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleCAP = `<?xml version="1.0" encoding="utf-8"?>
<cap:alert xmlns:cap="urn:oasis:names:tc:emergency:cap:1.2">
  <cap:identifier>urn:oid:2.49.0.1.404.0.2026.10.24.15.47.0</cap:identifier>
  <cap:sender>director@meteo.go.ke</cap:sender>
  <cap:sent>2026-10-24T18:47:00+03:00</cap:sent>
  <cap:status>Actual</cap:status>
  <cap:msgType>Alert</cap:msgType>
  <cap:scope>Public</cap:scope>
  <cap:info>
    <cap:language>en</cap:language>
    <cap:category>Met</cap:category>
    <cap:event>Heavy rainfall</cap:event>
    <cap:urgency>Expected</cap:urgency>
    <cap:severity>Severe</cap:severity>
    <cap:certainty>Likely</cap:certainty>
    <cap:onset>2026-10-24T18:00:00+03:00</cap:onset>
    <cap:expires>2026-10-30T09:00:00+03:00</cap:expires>
    <cap:headline>Heavy rainfall expected</cap:headline>
    <cap:description>Avoid flooded roads.</cap:description>
    <cap:instruction>Move to higher ground.</cap:instruction>
    <cap:web>https://meteo.go.ke/weather-warnings/example/</cap:web>
    <cap:area>
      <cap:areaDesc>Nairobi, Kiambu, Machakos</cap:areaDesc>
      <cap:polygon>-1.2,36.7 -1.3,36.9 -1.5,36.8 -1.2,36.7</cap:polygon>
    </cap:area>
  </cap:info>
</cap:alert>`

func TestParseCAP(t *testing.T) {
	alert, err := ParseCAP(strings.NewReader(sampleCAP))
	require.NoError(t, err)

	assert.Equal(t, "urn:oid:2.49.0.1.404.0.2026.10.24.15.47.0", alert.Identifier)
	assert.Equal(t, "Actual", alert.Status)
	assert.Equal(t, "Alert", alert.MsgType)
	require.Len(t, alert.Infos, 1)
	info := alert.Infos[0]
	assert.Equal(t, "Heavy rainfall", info.Event)
	assert.Equal(t, "Severe", info.Severity)
	assert.Equal(t, "Expected", info.Urgency)
	assert.Equal(t, "Likely", info.Certainty)
	assert.Equal(t, "Move to higher ground.", info.Instruction)
	assert.Equal(t, time.Date(2026, 10, 30, 6, 0, 0, 0, time.UTC), *info.Expires)
	require.Len(t, info.Areas, 1)
	assert.Equal(t, "Nairobi, Kiambu, Machakos", info.Areas[0].AreaDesc)
	require.Len(t, info.Areas[0].Polygons, 1)
	assert.Contains(t, info.Areas[0].Polygons[0], "-1.2,36.7")
}

func TestParseKMDCAPRSS(t *testing.T) {
	const rss = `<?xml version="1.0"?><rss version="2.0"><channel>
	  <title>Latest alerts from Kenya Meteorological Department</title>
	  <lastBuildDate>Sat, 24 Oct 2026 15:47:00 +0000</lastBuildDate>
	  <item>
	    <title>Heavy Rainfall Advisory</title>
	    <link>https://meteo.go.ke/api/cap/abc.xml</link>
	    <description>Rain expected.</description>
	    <pubDate>Sat, 24 Oct 2026 15:47:00 +0000</pubDate>
	    <guid>urn:oid:2.49.0.1.404.0.2026.10.24.15.47.0</guid>
	  </item>
	</channel></rss>`

	feed, err := ParseCAPRSS(strings.NewReader(rss))
	require.NoError(t, err)
	require.Len(t, feed.Items, 1)
	assert.Equal(t, "Heavy Rainfall Advisory", feed.Items[0].Title)
	assert.Equal(t, "urn:oid:2.49.0.1.404.0.2026.10.24.15.47.0", feed.Items[0].GUID)
	assert.Equal(t, "https://meteo.go.ke/api/cap/abc.xml", feed.Items[0].Link)
}

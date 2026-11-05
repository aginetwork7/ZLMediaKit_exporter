package collector

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/guohuachan/ZLMediaKit_exporter/internal/zlmapi"
)

func TestMacOf(t *testing.T) {
	tests := map[string]string{
		"aabbccddeeff-1-0":                        "aabbccddeeff",
		"AABBCCDDEEFF-2-1":                        "aabbccddeeff",
		"aabbccddeeff-1-0-1700000000-17000001-s1": "aabbccddeeff",
		"aabbccddeeff-1":                          "aabbccddeeff",
		"cam2":                                    "cam2",
	}
	for sourceID, want := range tests {
		assert.Equal(t, want, macOf(sourceID), sourceID)
	}
}

// fakeZLM serves a WebRTC topology the test can change between scrapes.
type fakeZLM struct {
	mu    sync.Mutex
	conns []zlmapi.WebRtcConnection
	batch zlmapi.WhipBatchInfo
	media []zlmapi.StreamInfo
}

func (f *fakeZLM) serve(t *testing.T) *httptest.Server {
	t.Helper()
	reply := func(w http.ResponseWriter, v any) {
		require.NoError(t, json.NewEncoder(w).Encode(v))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/"+zlmapi.EndpointGetWebRtcConnectionList, func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		reply(w, zlmapi.PagedResponse[[]zlmapi.WebRtcConnection]{Total: len(f.conns), Data: f.conns})
	})
	mux.HandleFunc("/"+zlmapi.EndpointWhipBatchInfo, func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		batch := f.batch
		reply(w, zlmapi.Response[zlmapi.WhipBatchLookup[zlmapi.WhipBatchInfo]]{
			Data: zlmapi.WhipBatchLookup[zlmapi.WhipBatchInfo]{Exists: true, Session: &batch},
		})
	})
	mux.HandleFunc("/"+zlmapi.EndpointGetMediaList, func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		reply(w, zlmapi.Response[[]zlmapi.StreamInfo]{Data: f.media})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeZLM) update(fn func(*fakeZLM)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func TestWebRtcCameraTrafficOutlivesConnections(t *testing.T) {
	const camera = "aabbccddeeff-1"
	player := func(id, stream string, sent uint64) zlmapi.WebRtcConnection {
		return zlmapi.WebRtcConnection{ID: id, SessionType: "whep_player", App: "nvr", Stream: stream, ParentID: "b1", SendBytes: sent}
	}
	media := func(stream string, created int, total uint64) zlmapi.StreamInfo {
		return zlmapi.StreamInfo{App: "nvr", Stream: stream, Schema: "rtsp", CreateStamp: created, TotalBytes: total}
	}

	zlm := &fakeZLM{
		conns: []zlmapi.WebRtcConnection{
			{ID: "b1", SessionType: sessionTypeWhipBatch, App: "nvr", Stream: "box01"},
			player("p1", camera+"-0", 1000),
			player("p2", camera+"-1", 2000),
		},
		batch: zlmapi.WhipBatchInfo{App: "nvr", Stream: "box01", Sources: []zlmapi.WhipBatchSource{
			{SourceID: camera + "-0", PlayApp: "nvr", PlayStream: camera + "-0"},
			{SourceID: camera + "-1", PlayApp: "nvr", PlayStream: camera + "-1"},
		}},
		media: []zlmapi.StreamInfo{media(camera+"-0", 1, 7000), media(camera+"-1", 1, 3000)},
	}
	srv := zlm.serve(t)

	exporter, err := New(srv.URL, testSecret, testLogger(), zlmapi.Options{})
	require.NoError(t, err)
	reg := prometheus.NewRegistry()
	reg.MustRegister(exporter)

	expect := func(egress, ingress float64, viewers int) {
		t.Helper()
		want := `
# HELP zlm_webrtc_camera_egress_bytes_total Bytes sent to players of any stream of the camera, accumulated by the exporter across connections
# TYPE zlm_webrtc_camera_egress_bytes_total counter
zlm_webrtc_camera_egress_bytes_total{mac="aabbccddeeff",nvr_id="box01"} ` + jsonNumber(egress) + `
# HELP zlm_webrtc_camera_ingress_bytes_total Bytes received for any stream of the camera, accumulated by the exporter across publisher sessions
# TYPE zlm_webrtc_camera_ingress_bytes_total counter
zlm_webrtc_camera_ingress_bytes_total{mac="aabbccddeeff",nvr_id="box01"} ` + jsonNumber(ingress) + `
# HELP zlm_webrtc_camera_viewers Players currently watching any stream of the camera
# TYPE zlm_webrtc_camera_viewers gauge
zlm_webrtc_camera_viewers{mac="aabbccddeeff",nvr_id="box01"} ` + jsonNumber(float64(viewers)) + `
`
		require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(want),
			"zlm_webrtc_camera_egress_bytes_total", "zlm_webrtc_camera_ingress_bytes_total", "zlm_webrtc_camera_viewers"))
	}

	expect(3000, 10000, 2)

	// p1 keeps sending, p2 leaves, p4 joins on another channel of the same
	// camera; the "-1" media source is recreated and restarts its byte count.
	zlm.update(func(f *fakeZLM) {
		f.conns = []zlmapi.WebRtcConnection{f.conns[0], player("p1", camera+"-0", 1500), player("p4", "aabbccddeeff-2-0", 100)}
		f.media = []zlmapi.StreamInfo{media(camera+"-0", 1, 7600), media(camera+"-1", 2, 400)}
	})
	expect(3600, 11000, 2)

	// Everyone leaves: the counters stay.
	zlm.update(func(f *fakeZLM) {
		f.conns = f.conns[:1]
	})
	expect(3600, 11000, 0)
}

func jsonNumber(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

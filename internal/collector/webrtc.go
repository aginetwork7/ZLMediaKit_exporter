package collector

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/guohuachan/ZLMediaKit_exporter/internal/zlmapi"
)

const (
	// webrtcPageSize is the largest page getWebRtcConnectionList serves.
	webrtcPageSize = 200
	// batchInfoConcurrency bounds the whipBatchInfo calls of one scrape.
	batchInfoConcurrency = 8
	// trafficIdleTTL is how long a camera's traffic counters outlive its last
	// binding, viewer or byte. Dropping a series earlier would make a returning
	// camera restart from zero, which Prometheus reads as a counter reset.
	trafficIdleTTL = time.Hour

	sessionTypeWhipBatch = "whip_batch_publisher"
	sessionTypeWhip      = "whip_publisher"
)

var (
	webrtcPublisherInfo = newMetricDescr(SubsystemWebRtc, "publisher_info",
		"whip/whip_batch publisher connection; stream is the NVR id for whip_batch",
		[]string{"id", "session_type", "signaling", "app", "stream", "local_candidate", "remote_candidate"})
	webrtcPublisherHealthy = newMetricDescr(SubsystemWebRtc, "publisher_healthy",
		"Whether the publisher saw traffic within its timeout (1: healthy, 0: unhealthy)",
		[]string{"id", "session_type", "app", "stream"})
	webrtcPublisherDuration = newMetricDescr(SubsystemWebRtc, "publisher_duration_seconds",
		"Publisher connection age", []string{"id", "session_type", "app", "stream"})
	webrtcPublisherViewers = newMetricDescr(SubsystemWebRtc, "publisher_viewers",
		"Players currently watching the publisher", []string{"id", "session_type", "app", "stream"})
	webrtcPublisherRecvBytes = newMetricDescr(SubsystemWebRtc, "publisher_received_bytes_total",
		"Bytes received on the publisher connection", []string{"id", "session_type", "app", "stream"})
	webrtcConnections = newMetricDescr(SubsystemWebRtc, "connections",
		"Number of WebRTC connections", []string{"session_type"})

	webrtcViewerInfo = newMetricDescr(SubsystemWebRtc, "viewer_info",
		"Player connection, attributed to the nvr_id/source_id it watches",
		[]string{"id", "session_type", "signaling", "app", "nvr_id", "source_id", "mac", "parent_id", "local_candidate", "remote_candidate"})
	webrtcViewerHealthy = newMetricDescr(SubsystemWebRtc, "viewer_healthy",
		"Whether the player saw traffic within its timeout (1: healthy, 0: unhealthy)", []string{"id", "nvr_id", "source_id"})
	webrtcViewerDuration = newMetricDescr(SubsystemWebRtc, "viewer_duration_seconds",
		"Player connection age", []string{"id", "nvr_id", "source_id"})

	webrtcCameraViewers = newMetricDescr(SubsystemWebRtc, "camera_viewers",
		"Players currently watching any stream of the camera", []string{"nvr_id", "mac"})
	webrtcCameraEgress = newMetricDescr(SubsystemWebRtc, "camera_egress_bytes_total",
		"Bytes sent to players of any stream of the camera, accumulated by the exporter across connections",
		[]string{"nvr_id", "mac"})
	webrtcCameraIngress = newMetricDescr(SubsystemWebRtc, "camera_ingress_bytes_total",
		"Bytes received for any stream of the camera, accumulated by the exporter across publisher sessions",
		[]string{"nvr_id", "mac"})
	webrtcEgress = newMetricDescr(SubsystemWebRtc, "egress_bytes_total",
		"Bytes sent by WebRTC connections, accumulated by the exporter; whip publishers are excluded",
		[]string{"session_type"})
	webrtcIngress = newMetricDescr(SubsystemWebRtc, "ingress_bytes_total",
		"Bytes received by WebRTC connections, accumulated by the exporter", []string{"session_type"})

	whipBatchInfo = newMetricDescr(SubsystemWhipBatch, "info",
		"whip_batch session information", []string{"app", "nvr_id", "transport_id", "state"})
	whipBatchSlots = newMetricDescr(SubsystemWhipBatch, "slots",
		"Negotiated audio/video slots", []string{"app", "nvr_id"})
	whipBatchFreeSlots = newMetricDescr(SubsystemWhipBatch, "free_slots",
		"Slots not bound to a source", []string{"app", "nvr_id"})
	whipBatchBoundSlots = newMetricDescr(SubsystemWhipBatch, "bound_slots",
		"Slots bound to a source", []string{"app", "nvr_id"})
	whipBatchReceivingSlots = newMetricDescr(SubsystemWhipBatch, "receiving_slots",
		"Slots receiving RTP", []string{"app", "nvr_id"})
	whipBatchViewers = newMetricDescr(SubsystemWhipBatch, "viewers",
		"Players watching any source of the session", []string{"app", "nvr_id"})
	whipBatchRecvBytes = newMetricDescr(SubsystemWhipBatch, "received_bytes_total",
		"Bytes received by the session's connection", []string{"app", "nvr_id"})

	whipBatchSlotInfo = newMetricDescr(SubsystemWhipBatch, "slot_info",
		"whip_batch slot bound to a source; slot_id is ZLMediaKit's streamId",
		[]string{"app", "nvr_id", "slot_id", "source_id", "state", "audio_mid", "video_mid", "audio_ssrc", "video_ssrc"})
	whipBatchSlotReceiving = newMetricDescr(SubsystemWhipBatch, "slot_receiving",
		"Whether the slot is receiving RTP", []string{"app", "nvr_id", "slot_id", "source_id"})

	whipBatchSourceInfo = newMetricDescr(SubsystemWhipBatch, "source_info",
		"Source bound to a whip_batch session",
		[]string{"app", "nvr_id", "source_id", "mac", "slot_id", "play_app", "play_stream"})
	whipBatchSourceOnline = newMetricDescr(SubsystemWhipBatch, "source_online",
		"Whether the bound source is online", []string{"app", "nvr_id", "source_id"})
	whipBatchSourceReceiving = newMetricDescr(SubsystemWhipBatch, "source_receiving",
		"Whether the source's slot is receiving RTP", []string{"app", "nvr_id", "source_id"})
	whipBatchSourceViewers = newMetricDescr(SubsystemWhipBatch, "source_viewers",
		"Players currently watching the source", []string{"app", "nvr_id", "source_id"})
	whipBatchSourceIngressSpeed = newMetricDescr(SubsystemWhipBatch, "source_ingress_bytes_per_second",
		"Current ingress speed of the source", []string{"app", "nvr_id", "source_id"})
)

// sourceKey identifies a published source. nvrID is empty for sources that
// are not part of a whip_batch session; sourceID is then the stream name.
type sourceKey struct {
	app, nvrID, sourceID string
}

// macOf extracts the camera MAC from a source id. The web player builds source
// ids as "<mac>-<remoteIndex>[-<streamId>[-<playback range>-<session>]]" with
// the colons stripped from the MAC; every source of a camera (each channel,
// stream quality, playback and two-way audio) shares it. An id without a dash
// is its own camera.
func macOf(sourceID string) string {
	mac, _, _ := strings.Cut(sourceID, "-")
	return strings.ToLower(mac)
}

// cameraKey is the unit of traffic accounting. The NVR is kept so a camera
// moved to another NVR or edge device stays traceable; sum by mac for the
// camera's total.
type cameraKey struct {
	nvrID, mac string
}

func (k sourceKey) camera() cameraKey {
	return cameraKey{nvrID: k.nvrID, mac: macOf(k.sourceID)}
}

type cameraTraffic struct {
	egress, ingress float64
	lastActive      time.Time
}

// connCursor remembers a connection's byte totals from the previous scrape
// and the source its traffic is attributed to.
type connCursor struct {
	recv, send uint64
	source     sourceKey
}

type mediaCursor struct {
	createStamp int
	total       uint64
	source      sourceKey
}

// webrtcCollector reports whip_batch sessions, their viewers and the traffic
// of each camera.
//
// ZLMediaKit only reports byte totals of live connections, so a camera's
// traffic disappears with its viewers. The collector folds the per-scrape
// growth of every connection into counters that outlive the connections; the
// bytes a connection moves after the last scrape before it closes are lost.
type webrtcCollector struct {
	now func() time.Time

	mu            sync.Mutex
	conns         map[string]*connCursor
	media         map[string]*mediaCursor
	cameras       map[cameraKey]*cameraTraffic
	egressByType  map[string]float64
	ingressByType map[string]float64
}

func newWebRtcCollector() *webrtcCollector {
	return &webrtcCollector{
		now:           time.Now,
		conns:         make(map[string]*connCursor),
		media:         make(map[string]*mediaCursor),
		cameras:       make(map[cameraKey]*cameraTraffic),
		egressByType:  make(map[string]float64),
		ingressByType: make(map[string]float64),
	}
}

func (*webrtcCollector) Endpoint() string { return zlmapi.EndpointGetWebRtcConnectionList }

func (*webrtcCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		webrtcPublisherInfo, webrtcPublisherHealthy, webrtcPublisherDuration, webrtcPublisherViewers,
		webrtcPublisherRecvBytes, webrtcConnections, webrtcViewerInfo, webrtcViewerHealthy, webrtcViewerDuration,
		webrtcCameraViewers, webrtcCameraEgress, webrtcCameraIngress, webrtcEgress, webrtcIngress,
		whipBatchInfo, whipBatchSlots, whipBatchFreeSlots, whipBatchBoundSlots, whipBatchReceivingSlots,
		whipBatchViewers, whipBatchRecvBytes, whipBatchSlotInfo, whipBatchSlotReceiving,
		whipBatchSourceInfo, whipBatchSourceOnline, whipBatchSourceReceiving, whipBatchSourceViewers,
		whipBatchSourceIngressSpeed,
	} {
		ch <- d
	}
}

// Collect fails only when the connection list is unavailable. Missing batch
// details or media list degrade the output and are reported as an error after
// the rest has been emitted.
func (c *webrtcCollector) Collect(ctx context.Context, client *zlmapi.Client, ch chan<- prometheus.Metric) error {
	conns, err := listWebRtcConnections(ctx, client)
	if err != nil {
		return err
	}
	batches, batchErr := fetchBatchInfos(ctx, client, conns)
	media, mediaErr := zlmapi.GetQuery[[]zlmapi.StreamInfo](ctx, client, zlmapi.EndpointGetMediaList,
		url.Values{"schema": {"rtsp"}})

	topo := newTopology(conns, batches)
	if mediaErr == nil {
		topo.media = make(map[string]zlmapi.StreamInfo, len(media))
		for _, m := range media {
			topo.media[mediaKey(m.App, m.Stream)] = m
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.account(topo)
	viewers := c.countViewers(topo)
	emitPublishers(topo, ch)
	c.emitViewers(topo, ch)
	emitBatches(topo, viewers, ch)
	c.emitTraffic(viewers, ch)

	if mediaErr != nil {
		mediaErr = fmt.Errorf("media list unavailable, whip_batch ingress not updated: %w", mediaErr)
	}
	return errors.Join(batchErr, mediaErr)
}

func listWebRtcConnections(ctx context.Context, client *zlmapi.Client) ([]zlmapi.WebRtcConnection, error) {
	var all []zlmapi.WebRtcConnection
	seen := make(map[string]bool)
	for offset := 0; ; offset += webrtcPageSize {
		page, total, err := zlmapi.GetPage[[]zlmapi.WebRtcConnection](ctx, client, zlmapi.EndpointGetWebRtcConnectionList,
			url.Values{"offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(webrtcPageSize)}})
		if err != nil {
			return nil, err
		}
		// Connections opening or closing between pages shift the offsets.
		for _, conn := range page {
			if !seen[conn.ID] {
				seen[conn.ID] = true
				all = append(all, conn)
			}
		}
		if len(page) == 0 || offset+webrtcPageSize >= total {
			return all, nil
		}
	}
}

// fetchBatchInfos returns the details of every whip_batch session, keyed by
// app and NVR id. Sessions whose details failed are left out.
func fetchBatchInfos(ctx context.Context, client *zlmapi.Client, conns []zlmapi.WebRtcConnection) (map[string]*zlmapi.WhipBatchInfo, error) {
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		errs    []error
		infos   = make(map[string]*zlmapi.WhipBatchInfo)
		limiter = make(chan struct{}, batchInfoConcurrency)
	)
	for _, conn := range conns {
		if conn.SessionType != sessionTypeWhipBatch {
			continue
		}
		wg.Add(1)
		go func(app, nvrID string) {
			defer wg.Done()
			limiter <- struct{}{}
			defer func() { <-limiter }()

			lookup, err := zlmapi.GetQuery[zlmapi.WhipBatchLookup[zlmapi.WhipBatchInfo]](ctx, client,
				zlmapi.EndpointWhipBatchInfo, url.Values{"app": {app}, "stream": {nvrID}})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("whip_batch %s/%s: %w", app, nvrID, err))
				return
			}
			if lookup.Exists && lookup.Session != nil {
				infos[mediaKey(app, nvrID)] = lookup.Session
			}
		}(conn.App, conn.Stream)
	}
	wg.Wait()
	return infos, errors.Join(errs...)
}

// topology is one scrape's view of publishers, their sources and viewers.
type topology struct {
	conns   []zlmapi.WebRtcConnection
	byID    map[string]*zlmapi.WebRtcConnection
	batches map[string]*zlmapi.WhipBatchInfo
	// bound maps a batch source's play app/stream to its source.
	bound map[string]sourceKey
	// media holds the rtsp media sources; nil when the media list failed.
	media map[string]zlmapi.StreamInfo
}

func newTopology(conns []zlmapi.WebRtcConnection, batches map[string]*zlmapi.WhipBatchInfo) *topology {
	sort.Slice(conns, func(i, j int) bool { return conns[i].ID < conns[j].ID })
	t := &topology{
		conns:   conns,
		byID:    make(map[string]*zlmapi.WebRtcConnection, len(conns)),
		batches: batches,
		bound:   make(map[string]sourceKey),
	}
	for i := range conns {
		t.byID[conns[i].ID] = &conns[i]
	}
	for _, info := range batches {
		for _, src := range info.Sources {
			t.bound[mediaKey(src.PlayApp, src.PlayStream)] = sourceKey{app: info.App, nvrID: info.Stream, sourceID: src.SourceID}
		}
	}
	return t
}

// sourceOf attributes a player or whip publisher to the source it plays or
// publishes.
func (t *topology) sourceOf(conn *zlmapi.WebRtcConnection) sourceKey {
	if isPlayer(conn.SessionType) {
		if parent := t.byID[conn.ParentID]; parent != nil && parent.SessionType == sessionTypeWhipBatch {
			return sourceKey{app: conn.App, nvrID: parent.Stream, sourceID: conn.Stream}
		}
		if src, ok := t.bound[mediaKey(conn.App, conn.Stream)]; ok {
			return src
		}
	}
	return sourceKey{app: conn.App, sourceID: conn.Stream}
}

func isPlayer(sessionType string) bool { return strings.HasSuffix(sessionType, "_player") }

// isWhipPublisher reports the session types whose sendBytes ZLMediaKit
// replaces with their viewers' total.
func isWhipPublisher(sessionType string) bool { return strings.HasPrefix(sessionType, "whip") }

func mediaKey(app, stream string) string { return app + "\n" + stream }

// growth is how much a byte total grew since the previous scrape. A total
// below the previous one belongs to a new connection or media source.
func growth(prev, cur uint64, first bool) float64 {
	if first || cur < prev {
		return float64(cur)
	}
	return float64(cur - prev)
}

func (c *webrtcCollector) touch(src sourceKey, now time.Time) *cameraTraffic {
	key := src.camera()
	ct := c.cameras[key]
	if ct == nil {
		ct = &cameraTraffic{}
		c.cameras[key] = ct
	}
	ct.lastActive = now
	return ct
}

// account folds the scrape's byte totals into the exporter's counters.
func (c *webrtcCollector) account(t *topology) {
	now := c.now()

	for _, src := range t.bound {
		c.touch(src, now)
	}

	seenConns := make(map[string]bool, len(t.conns))
	for i := range t.conns {
		conn := &t.conns[i]
		seenConns[conn.ID] = true

		src := t.sourceOf(conn)
		cur, ok := c.conns[conn.ID]
		if !ok {
			cur = &connCursor{source: src}
			c.conns[conn.ID] = cur
		} else if cur.source.nvrID == "" && src.nvrID != "" {
			// The batch binding showed up after the player connected.
			cur.source = src
		}

		recv := growth(cur.recv, conn.RecvBytes, !ok)
		send := growth(cur.send, conn.SendBytes, !ok)
		cur.recv, cur.send = conn.RecvBytes, conn.SendBytes

		c.ingressByType[conn.SessionType] += recv
		if !isWhipPublisher(conn.SessionType) {
			c.egressByType[conn.SessionType] += send
		}

		switch {
		case isPlayer(conn.SessionType):
			c.touch(cur.source, now).egress += send
		case conn.SessionType == sessionTypeWhip:
			c.touch(cur.source, now).ingress += recv
		}
	}
	for id := range c.conns {
		if !seenConns[id] {
			delete(c.conns, id)
		}
	}

	// whip_batch ingress is only known per media source.
	if t.media != nil {
		seenMedia := make(map[string]bool, len(t.media))
		for key, m := range t.media {
			cur, ok := c.media[key]
			src, bound := t.bound[key]
			if !ok && !bound {
				continue
			}
			first := !ok || cur.createStamp != m.CreateStamp
			if !ok {
				cur = &mediaCursor{source: src}
				c.media[key] = cur
			} else if bound {
				cur.source = src
			}
			seenMedia[key] = true

			in := growth(cur.total, m.TotalBytes, first)
			cur.createStamp, cur.total = m.CreateStamp, m.TotalBytes
			if in > 0 {
				c.touch(cur.source, now).ingress += in
			}
		}
		for key := range c.media {
			if !seenMedia[key] {
				delete(c.media, key)
			}
		}
	}

	for key, ct := range c.cameras {
		if now.Sub(ct.lastActive) > trafficIdleTTL {
			delete(c.cameras, key)
		}
	}
}

// countViewers counts the players attributed to each source.
func (c *webrtcCollector) countViewers(t *topology) map[sourceKey]int {
	viewers := make(map[sourceKey]int)
	for i := range t.conns {
		if !isPlayer(t.conns[i].SessionType) {
			continue
		}
		if cur := c.conns[t.conns[i].ID]; cur != nil {
			viewers[cur.source]++
		}
	}
	return viewers
}

func emitPublishers(t *topology, ch chan<- prometheus.Metric) {
	byType := make(map[string]int)
	for i := range t.conns {
		conn := &t.conns[i]
		byType[conn.SessionType]++
		if !isWhipPublisher(conn.SessionType) {
			continue
		}

		ch <- prometheus.MustNewConstMetric(webrtcPublisherInfo, prometheus.GaugeValue, 1,
			conn.ID, conn.SessionType, conn.SignalingProtocol, conn.App, conn.Stream, conn.LocalCandidate, conn.RemoteCandidate)

		labels := []string{conn.ID, conn.SessionType, conn.App, conn.Stream}
		ch <- prometheus.MustNewConstMetric(webrtcPublisherHealthy, prometheus.GaugeValue, boolValue(conn.Health == "healthy"), labels...)
		ch <- prometheus.MustNewConstMetric(webrtcPublisherDuration, prometheus.GaugeValue, float64(conn.DurationSec), labels...)
		ch <- prometheus.MustNewConstMetric(webrtcPublisherViewers, prometheus.GaugeValue, float64(conn.ViewerCount), labels...)
		ch <- prometheus.MustNewConstMetric(webrtcPublisherRecvBytes, prometheus.CounterValue, float64(conn.RecvBytes), labels...)
	}

	for _, sessionType := range sortedKeys(byType) {
		ch <- prometheus.MustNewConstMetric(webrtcConnections, prometheus.GaugeValue, float64(byType[sessionType]), sessionType)
	}
}

func (c *webrtcCollector) emitViewers(t *topology, ch chan<- prometheus.Metric) {
	for i := range t.conns {
		conn := &t.conns[i]
		cur := c.conns[conn.ID]
		if !isPlayer(conn.SessionType) || cur == nil {
			continue
		}
		src := cur.source
		ch <- prometheus.MustNewConstMetric(webrtcViewerInfo, prometheus.GaugeValue, 1,
			conn.ID, conn.SessionType, conn.SignalingProtocol, conn.App, src.nvrID, src.sourceID, macOf(src.sourceID),
			conn.ParentID, conn.LocalCandidate, conn.RemoteCandidate)
		ch <- prometheus.MustNewConstMetric(webrtcViewerHealthy, prometheus.GaugeValue,
			boolValue(conn.Health == "healthy"), conn.ID, src.nvrID, src.sourceID)
		ch <- prometheus.MustNewConstMetric(webrtcViewerDuration, prometheus.GaugeValue,
			float64(conn.DurationSec), conn.ID, src.nvrID, src.sourceID)
	}
}

func emitBatches(t *topology, viewers map[sourceKey]int, ch chan<- prometheus.Metric) {
	for i := range t.conns {
		conn := &t.conns[i]
		if conn.SessionType != sessionTypeWhipBatch {
			continue
		}
		app, nvrID := conn.App, conn.Stream

		ch <- prometheus.MustNewConstMetric(whipBatchViewers, prometheus.GaugeValue, float64(conn.ViewerCount), app, nvrID)
		ch <- prometheus.MustNewConstMetric(whipBatchRecvBytes, prometheus.CounterValue, float64(conn.RecvBytes), app, nvrID)

		if conn.Batch != nil && conn.Batch.Exists && conn.Batch.Session != nil {
			s := conn.Batch.Session
			ch <- prometheus.MustNewConstMetric(whipBatchInfo, prometheus.GaugeValue, 1, app, nvrID, s.TransportID, s.State)
			ch <- prometheus.MustNewConstMetric(whipBatchSlots, prometheus.GaugeValue, float64(s.StreamCount), app, nvrID)
			ch <- prometheus.MustNewConstMetric(whipBatchFreeSlots, prometheus.GaugeValue, float64(s.FreeStreamCount), app, nvrID)
			ch <- prometheus.MustNewConstMetric(whipBatchBoundSlots, prometheus.GaugeValue, float64(s.BoundStreamCount), app, nvrID)
			ch <- prometheus.MustNewConstMetric(whipBatchReceivingSlots, prometheus.GaugeValue, float64(s.ReceivingStreamCount), app, nvrID)
		}

		info := t.batches[mediaKey(app, nvrID)]
		if info == nil {
			continue
		}

		receivingBySlot := make(map[string]bool, len(info.Streams))
		for _, slot := range info.Streams {
			// A session negotiates dozens of slots up front; free ones are
			// already counted by whip_batch_free_slots.
			if slot.SourceID == "" && !slot.Receiving {
				continue
			}
			receivingBySlot[slot.StreamID] = slot.Receiving
			ch <- prometheus.MustNewConstMetric(whipBatchSlotInfo, prometheus.GaugeValue, 1,
				app, nvrID, slot.StreamID, slot.SourceID, slot.State, slot.AudioMid, slot.VideoMid,
				strconv.FormatUint(slot.AudioSsrc, 10), strconv.FormatUint(slot.VideoSsrc, 10))
			ch <- prometheus.MustNewConstMetric(whipBatchSlotReceiving, prometheus.GaugeValue,
				boolValue(slot.Receiving), app, nvrID, slot.StreamID, slot.SourceID)
		}

		sources := append([]zlmapi.WhipBatchSource(nil), info.Sources...)
		sort.Slice(sources, func(i, j int) bool { return sources[i].SourceID < sources[j].SourceID })
		for _, src := range sources {
			key := sourceKey{app: app, nvrID: nvrID, sourceID: src.SourceID}
			ch <- prometheus.MustNewConstMetric(whipBatchSourceInfo, prometheus.GaugeValue, 1,
				app, nvrID, src.SourceID, macOf(src.SourceID), src.StreamID, src.PlayApp, src.PlayStream)
			ch <- prometheus.MustNewConstMetric(whipBatchSourceOnline, prometheus.GaugeValue,
				boolValue(src.Online), app, nvrID, src.SourceID)
			ch <- prometheus.MustNewConstMetric(whipBatchSourceReceiving, prometheus.GaugeValue,
				boolValue(receivingBySlot[src.StreamID]), app, nvrID, src.SourceID)
			ch <- prometheus.MustNewConstMetric(whipBatchSourceViewers, prometheus.GaugeValue,
				float64(viewers[key]), app, nvrID, src.SourceID)
			if m, ok := t.media[mediaKey(src.PlayApp, src.PlayStream)]; ok {
				ch <- prometheus.MustNewConstMetric(whipBatchSourceIngressSpeed, prometheus.GaugeValue,
					m.BytesSpeed, app, nvrID, src.SourceID)
			}
		}
	}
}

func (c *webrtcCollector) emitTraffic(viewers map[sourceKey]int, ch chan<- prometheus.Metric) {
	cameraViewers := make(map[cameraKey]int)
	for src, n := range viewers {
		cameraViewers[src.camera()] += n
	}

	keys := make([]cameraKey, 0, len(c.cameras))
	for key := range c.cameras {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].nvrID != keys[j].nvrID {
			return keys[i].nvrID < keys[j].nvrID
		}
		return keys[i].mac < keys[j].mac
	})
	for _, key := range keys {
		ct := c.cameras[key]
		ch <- prometheus.MustNewConstMetric(webrtcCameraViewers, prometheus.GaugeValue, float64(cameraViewers[key]), key.nvrID, key.mac)
		ch <- prometheus.MustNewConstMetric(webrtcCameraEgress, prometheus.CounterValue, ct.egress, key.nvrID, key.mac)
		ch <- prometheus.MustNewConstMetric(webrtcCameraIngress, prometheus.CounterValue, ct.ingress, key.nvrID, key.mac)
	}

	for _, sessionType := range sortedKeys(c.egressByType) {
		ch <- prometheus.MustNewConstMetric(webrtcEgress, prometheus.CounterValue, c.egressByType[sessionType], sessionType)
	}
	for _, sessionType := range sortedKeys(c.ingressByType) {
		ch <- prometheus.MustNewConstMetric(webrtcIngress, prometheus.CounterValue, c.ingressByType[sessionType], sessionType)
	}
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

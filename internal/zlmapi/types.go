package zlmapi

// Version is the payload of index/api/version.
type Version struct {
	BranchName string `json:"branchName"`
	BuildTime  string `json:"buildTime"`
	CommitHash string `json:"commitHash"`
}

// ThreadLoad is one entry of index/api/getThreadsLoad and
// index/api/getWorkThreadsLoad.
type ThreadLoad struct {
	Load  float64 `json:"load"`
	Delay float64 `json:"delay"`
}

// Statistics is the payload of index/api/getStatistic: live counts of
// ZLMediaKit's internal object pools.
type Statistics struct {
	Buffer                float64 `json:"Buffer"`
	BufferLikeString      float64 `json:"BufferLikeString"`
	BufferList            float64 `json:"BufferList"`
	BufferRaw             float64 `json:"BufferRaw"`
	Frame                 float64 `json:"Frame"`
	FrameImp              float64 `json:"FrameImp"`
	MediaSource           float64 `json:"MediaSource"`
	MultiMediaSourceMuxer float64 `json:"MultiMediaSourceMuxer"`
	RtmpPacket            float64 `json:"RtmpPacket"`
	RtpPacket             float64 `json:"RtpPacket"`
	Socket                float64 `json:"Socket"`
	TcpClient             float64 `json:"TcpClient"`
	TcpServer             float64 `json:"TcpServer"`
	TcpSession            float64 `json:"TcpSession"`
	UdpServer             float64 `json:"UdpServer"`
	UdpSession            float64 `json:"UdpSession"`
}

// Session is one entry of index/api/getAllSession.
type Session struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	LocalIP    string `json:"local_ip"`
	LocalPort  int    `json:"local_port"`
	PeerIP     string `json:"peer_ip"`
	PeerPort   int    `json:"peer_port"`
	TypeID     string `json:"typeid"`
}

// StreamInfo is one entry of index/api/getMediaList. A source stream is
// published under several schemas, so one stream yields one entry per schema.
type StreamInfo struct {
	AliveSecond      int     `json:"aliveSecond"`
	App              string  `json:"app"`
	BytesSpeed       float64 `json:"bytesSpeed"`
	TotalBytes       uint64  `json:"totalBytes"`
	CreateStamp      int     `json:"createStamp"`
	OriginType       int     `json:"originType"`
	OriginTypeStr    string  `json:"originTypeStr"`
	OriginURL        string  `json:"originUrl"`
	ReaderCount      int     `json:"readerCount"`
	Schema           string  `json:"schema"`
	Stream           string  `json:"stream"`
	TotalReaderCount int     `json:"totalReaderCount"`
	Vhost            string  `json:"vhost"`
}

// RtpServer is one entry of index/api/listRtpServer.
type RtpServer struct {
	Port     string `json:"port"`
	StreamID string `json:"stream_id"`
}

// WebRtcConnection is one entry of index/api/getWebRtcConnectionList.
//
// For whip publishers ZLMediaKit replaces SendBytes with the sum of their
// viewers' SendBytes, so only player entries report their own egress.
type WebRtcConnection struct {
	ID                string `json:"id"`
	SessionType       string `json:"sessionType"`
	SignalingProtocol string `json:"signalingProtocol"`
	Health            string `json:"health"`
	LocalCandidate    string `json:"localCandidate"`
	RemoteCandidate   string `json:"remoteCandidate"`
	CreateTime        uint64 `json:"createTime"` // milliseconds since epoch
	DurationSec       uint64 `json:"durationSec"`
	RecvBytes         uint64 `json:"recvBytes"`
	SendBytes         uint64 `json:"sendBytes"`
	App               string `json:"app"`
	Stream            string `json:"stream"`
	// ParentID is set on whep players watching a whip or whip_batch publisher.
	ParentID    string `json:"parentId"`
	ViewerCount uint64 `json:"viewerCount"`
	// Batch is set on whip_batch publishers.
	Batch *WhipBatchLookup[WhipBatchSummary] `json:"batch"`
}

// WhipBatchLookup wraps a batch session that may have closed in between.
type WhipBatchLookup[T any] struct {
	Exists  bool `json:"exists"`
	Session *T   `json:"session"`
}

// WhipBatchSummary is the per-session summary embedded in the connection list.
// Stream is the NVR id the batch was published under.
type WhipBatchSummary struct {
	App                  string `json:"app"`
	Stream               string `json:"stream"`
	TransportID          string `json:"transportId"`
	StreamCount          uint64 `json:"streamCount"`
	State                string `json:"state"`
	FreeStreamCount      uint64 `json:"freeStreamCount"`
	BoundStreamCount     uint64 `json:"boundStreamCount"`
	ReceivingStreamCount uint64 `json:"receivingStreamCount"`
}

// WhipBatchInfo is the session detail of index/api/whipBatchInfo.
type WhipBatchInfo struct {
	App             string            `json:"app"`
	Stream          string            `json:"stream"`
	TransportID     string            `json:"transportId"`
	StreamCount     uint64            `json:"streamCount"`
	State           string            `json:"state"`
	FreeStreamCount uint64            `json:"freeStreamCount"`
	Streams         []WhipBatchSlot   `json:"streams"`
	Sources         []WhipBatchSource `json:"sources"`
	FreeStreams     []string          `json:"freeStreams"`
}

// WhipBatchSlot is one negotiated audio/video pair of a batch session.
type WhipBatchSlot struct {
	StreamID  string `json:"streamId"`
	AudioMid  string `json:"audioMid"`
	VideoMid  string `json:"videoMid"`
	AudioSsrc uint64 `json:"audioSsrc"`
	VideoSsrc uint64 `json:"videoSsrc"`
	SourceID  string `json:"sourceId"`
	Receiving bool   `json:"receiving"`
	State     string `json:"state"`
}

// WhipBatchSource binds a source (camera) to a slot. Viewers play it at
// PlayApp/PlayStream.
type WhipBatchSource struct {
	SourceID   string `json:"sourceId"`
	StreamID   string `json:"streamId"`
	PlayApp    string `json:"playApp"`
	PlayStream string `json:"playStream"`
	Online     bool   `json:"online"`
}

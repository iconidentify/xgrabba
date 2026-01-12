package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// VideoProcessor handles video analysis using ffmpeg.
type VideoProcessor struct {
	ffmpegPath  string
	ffprobePath string
}

// NewVideoProcessor creates a new video processor.
// It will attempt to find ffmpeg and ffprobe in PATH.
func NewVideoProcessor() (*VideoProcessor, error) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found in PATH: %w", err)
	}

	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil, fmt.Errorf("ffprobe not found in PATH: %w", err)
	}

	return &VideoProcessor{
		ffmpegPath:  ffmpegPath,
		ffprobePath: ffprobePath,
	}, nil
}

// VideoInfo contains metadata about a video file.
type VideoInfo struct {
	Duration   float64 // Duration in seconds
	Width      int
	Height     int
	HasAudio   bool
	AudioCodec string
	VideoCodec string
	Bitrate    int64
	FrameRate  float64
	FileSize   int64
}

// GetVideoInfo extracts metadata from a video file.
func (p *VideoProcessor) GetVideoInfo(ctx context.Context, videoPath string) (*VideoInfo, error) {
	// Get file size
	stat, err := os.Stat(videoPath)
	if err != nil {
		return nil, fmt.Errorf("stat video: %w", err)
	}

	// Use ffprobe to get video info
	cmd := exec.CommandContext(ctx, p.ffprobePath,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		videoPath,
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}

	info := &VideoInfo{
		FileSize: stat.Size(),
	}

	// Parse ffprobe JSON (robust) and fall back to old string parsing if needed.
	type ffprobeFormat struct {
		Duration string `json:"duration"`
		BitRate  string `json:"bit_rate"`
	}
	type ffprobeStream struct {
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		AvgFrameRate string `json:"avg_frame_rate"`
	}
	type ffprobeOutput struct {
		Format  ffprobeFormat   `json:"format"`
		Streams []ffprobeStream `json:"streams"`
	}

	var parsed ffprobeOutput
	if err := json.Unmarshal(output, &parsed); err == nil {
		if parsed.Format.Duration != "" {
			if dur, err := strconv.ParseFloat(parsed.Format.Duration, 64); err == nil {
				info.Duration = dur
			}
		}

		if parsed.Format.BitRate != "" {
			if br, err := strconv.ParseInt(parsed.Format.BitRate, 10, 64); err == nil {
				info.Bitrate = br
			}
		}

		for _, s := range parsed.Streams {
			switch s.CodecType {
			case "audio":
				info.HasAudio = true
				if info.AudioCodec == "" {
					info.AudioCodec = s.CodecName
				}
			case "video":
				if info.VideoCodec == "" {
					info.VideoCodec = s.CodecName
				}
				if info.Width == 0 && s.Width > 0 {
					info.Width = s.Width
				}
				if info.Height == 0 && s.Height > 0 {
					info.Height = s.Height
				}
				if info.FrameRate == 0 && s.AvgFrameRate != "" && s.AvgFrameRate != "0/0" {
					parts := strings.SplitN(s.AvgFrameRate, "/", 2)
					if len(parts) == 2 {
						num, err1 := strconv.ParseFloat(parts[0], 64)
						den, err2 := strconv.ParseFloat(parts[1], 64)
						if err1 == nil && err2 == nil && den != 0 {
							info.FrameRate = num / den
						}
					}
				}
			}
		}
	} else {
		// Legacy string parsing fallback
		outputStr := string(output)

		if idx := strings.Index(outputStr, `"duration"`); idx != -1 {
			rest := outputStr[idx+11:]
			if endIdx := strings.Index(rest, `"`); endIdx != -1 {
				if dur, err := strconv.ParseFloat(rest[:endIdx], 64); err == nil {
					info.Duration = dur
				}
			}
		}

		info.HasAudio = strings.Contains(outputStr, `"codec_type": "audio"`) ||
			strings.Contains(outputStr, `"codec_type":"audio"`)

		if idx := strings.Index(outputStr, `"width"`); idx != -1 {
			rest := outputStr[idx+8:]
			if endIdx := strings.IndexAny(rest, ",}"); endIdx != -1 {
				if w, err := strconv.Atoi(strings.TrimSpace(rest[:endIdx])); err == nil {
					info.Width = w
				}
			}
		}

		if idx := strings.Index(outputStr, `"height"`); idx != -1 {
			rest := outputStr[idx+9:]
			if endIdx := strings.IndexAny(rest, ",}"); endIdx != -1 {
				if h, err := strconv.Atoi(strings.TrimSpace(rest[:endIdx])); err == nil {
					info.Height = h
				}
			}
		}
	}

	return info, nil
}

// ExtractKeyframesConfig configures keyframe extraction.
type ExtractKeyframesConfig struct {
	IntervalSeconds int    // Extract a frame every N seconds (default: 10)
	MaxFrames       int    // Maximum number of frames to extract (default: 20)
	MaxWidth        int    // Maximum width of extracted frames (default: 1280)
	Quality         int    // JPEG quality 1-31, lower is better (default: 5)
	OutputDir       string // Directory to save frames
}

// ExtractKeyframes extracts keyframes from a video at regular intervals.
// Returns paths to the extracted frame images.
func (p *VideoProcessor) ExtractKeyframes(ctx context.Context, videoPath string, cfg ExtractKeyframesConfig) ([]string, error) {
	// Set defaults
	if cfg.IntervalSeconds <= 0 {
		cfg.IntervalSeconds = 10
	}
	if cfg.MaxFrames <= 0 {
		cfg.MaxFrames = 20
	}
	if cfg.MaxWidth <= 0 {
		cfg.MaxWidth = 1280
	}
	if cfg.Quality <= 0 {
		cfg.Quality = 5
	}

	// Get video duration to calculate frame extraction
	info, err := p.GetVideoInfo(ctx, videoPath)
	if err != nil {
		return nil, fmt.Errorf("get video info: %w", err)
	}

	// If duration is unknown/zero, fall back to fps-based extraction so we still get frames.
	// This avoids the "timestamp >= duration" early break that would otherwise yield 0 frames.
	if info.Duration <= 0 {
		if err := os.MkdirAll(cfg.OutputDir, 0755); err != nil {
			return nil, fmt.Errorf("create output dir: %w", err)
		}

		outputPattern := filepath.Join(cfg.OutputDir, "frame_%03d.jpg")
		// Extract frames at a steady rate, capped by MaxFrames.
		// fps=1/N extracts one frame every N seconds.
		cmd := exec.CommandContext(ctx, p.ffmpegPath,
			"-i", videoPath,
			"-vf", fmt.Sprintf("fps=1/%d,scale='min(%d,iw)':-1", cfg.IntervalSeconds, cfg.MaxWidth),
			"-q:v", strconv.Itoa(cfg.Quality),
			"-frames:v", strconv.Itoa(cfg.MaxFrames),
			"-y",
			outputPattern,
		)
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("extract keyframes (fps fallback): %w", err)
		}

		entries, err := os.ReadDir(cfg.OutputDir)
		if err != nil {
			return nil, fmt.Errorf("read frames dir: %w", err)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		var frames []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext == ".jpg" || ext == ".jpeg" {
				frames = append(frames, filepath.Join(cfg.OutputDir, e.Name()))
			}
		}
		if len(frames) == 0 {
			return nil, fmt.Errorf("no frames extracted from video (fps fallback)")
		}
		return frames, nil
	}

	// Calculate how many frames we'll extract
	numFrames := int(info.Duration) / cfg.IntervalSeconds
	if numFrames > cfg.MaxFrames {
		numFrames = cfg.MaxFrames
		// Adjust interval to spread frames evenly
		cfg.IntervalSeconds = int(info.Duration) / numFrames
	}
	if numFrames < 1 {
		numFrames = 1
	}

	// Create output directory
	if err := os.MkdirAll(cfg.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	var frames []string

	// Extract frames at intervals
	for i := 0; i < numFrames; i++ {
		select {
		case <-ctx.Done():
			return frames, ctx.Err()
		default:
		}

		timestamp := float64(i * cfg.IntervalSeconds)
		if timestamp >= info.Duration {
			break
		}

		outputPath := filepath.Join(cfg.OutputDir, fmt.Sprintf("frame_%03d.jpg", i))

		// ffmpeg command to extract a single frame
		cmd := exec.CommandContext(ctx, p.ffmpegPath,
			"-i", videoPath,
			// Seek after opening input for better compatibility with some container/codec combinations.
			"-ss", fmt.Sprintf("%.2f", timestamp),
			"-vframes", "1",
			"-vf", fmt.Sprintf("scale='min(%d,iw)':-1", cfg.MaxWidth),
			"-q:v", strconv.Itoa(cfg.Quality),
			"-y", // Overwrite output
			outputPath,
		)

		if err := cmd.Run(); err != nil {
			// Skip frames that fail (might be past end of video)
			continue
		}

		// Verify the frame was created
		if _, err := os.Stat(outputPath); err == nil {
			frames = append(frames, outputPath)
		}
	}

	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames extracted from video")
	}

	return frames, nil
}

// ExtractAudioConfig configures audio extraction.
type ExtractAudioConfig struct {
	OutputPath     string // Path for output audio file
	Format         string // Output format: "mp3", "wav", "m4a" (default: "mp3")
	SampleRate     int    // Sample rate in Hz (default: 16000 for speech)
	Channels       int    // Number of channels, 1=mono (default: 1)
	Bitrate        string // Audio bitrate (default: "64k")
	MaxDurationSec int    // Max duration to extract in seconds (0 = full)
}

// ExtractAudio extracts the audio track from a video file.
// Returns the path to the extracted audio file and its duration.
func (p *VideoProcessor) ExtractAudio(ctx context.Context, videoPath string, cfg ExtractAudioConfig) (string, float64, error) {
	// Set defaults
	if cfg.Format == "" {
		cfg.Format = "mp3"
	}
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 16000 // 16kHz is good for speech recognition
	}
	if cfg.Channels <= 0 {
		cfg.Channels = 1 // Mono for speech
	}
	if cfg.Bitrate == "" {
		cfg.Bitrate = "64k"
	}

	// Get video info to check for audio
	info, err := p.GetVideoInfo(ctx, videoPath)
	if err != nil {
		return "", 0, fmt.Errorf("get video info: %w", err)
	}

	if !info.HasAudio {
		return "", 0, fmt.Errorf("video has no audio track")
	}

	// Create output directory
	if err := os.MkdirAll(filepath.Dir(cfg.OutputPath), 0755); err != nil {
		return "", 0, fmt.Errorf("create output dir: %w", err)
	}

	// Build ffmpeg command
	args := []string{
		"-i", videoPath,
		"-vn", // No video
		"-acodec", getAudioCodec(cfg.Format),
		"-ar", strconv.Itoa(cfg.SampleRate),
		"-ac", strconv.Itoa(cfg.Channels),
		"-b:a", cfg.Bitrate,
	}

	// Add duration limit if specified
	if cfg.MaxDurationSec > 0 {
		args = append(args, "-t", strconv.Itoa(cfg.MaxDurationSec))
	}

	args = append(args, "-y", cfg.OutputPath)

	cmd := exec.CommandContext(ctx, p.ffmpegPath, args...)
	if err := cmd.Run(); err != nil {
		return "", 0, fmt.Errorf("extract audio: %w", err)
	}

	// Get duration of extracted audio by probing the OUTPUT audio file.
	// This is more reliable than using the input container duration (which can be missing/0).
	audioDuration := 0.0
	if outInfo, err := p.GetVideoInfo(ctx, cfg.OutputPath); err == nil && outInfo.Duration > 0 {
		audioDuration = outInfo.Duration
	} else {
		// Fallback: use input duration if we have it
		audioDuration = info.Duration
	}
	if cfg.MaxDurationSec > 0 && audioDuration > 0 && float64(cfg.MaxDurationSec) < audioDuration {
		audioDuration = float64(cfg.MaxDurationSec)
	}

	return cfg.OutputPath, audioDuration, nil
}

func getAudioCodec(format string) string {
	switch format {
	case "mp3":
		return "libmp3lame"
	case "wav":
		return "pcm_s16le"
	case "m4a":
		return "aac"
	case "ogg":
		return "libvorbis"
	default:
		return "libmp3lame"
	}
}

// ChunkAudioConfig configures audio chunking for large files.
type ChunkAudioConfig struct {
	ChunkDurationSec int    // Duration of each chunk in seconds (default: 300 = 5 min)
	OutputDir        string // Directory to save chunks
	Format           string // Output format (default: "mp3")
}

// ChunkAudio splits an audio file into smaller chunks.
// This is useful for working with APIs that have file size limits (e.g., OpenAI's 25MB limit).
// Returns paths to the chunk files.
func (p *VideoProcessor) ChunkAudio(ctx context.Context, audioPath string, cfg ChunkAudioConfig) ([]string, error) {
	// Set defaults
	if cfg.ChunkDurationSec <= 0 {
		cfg.ChunkDurationSec = 300 // 5 minutes
	}
	if cfg.Format == "" {
		cfg.Format = "mp3"
	}

	// Get audio duration
	info, err := p.GetVideoInfo(ctx, audioPath)
	if err != nil {
		return nil, fmt.Errorf("get audio info: %w", err)
	}

	// If file is small enough, just return it
	stat, err := os.Stat(audioPath)
	if err != nil {
		return nil, fmt.Errorf("stat audio: %w", err)
	}
	if stat.Size() < 20*1024*1024 { // Under 20MB, safe for most APIs
		return []string{audioPath}, nil
	}

	// Create output directory
	if err := os.MkdirAll(cfg.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	var chunks []string
	numChunks := int(info.Duration/float64(cfg.ChunkDurationSec)) + 1

	for i := 0; i < numChunks; i++ {
		select {
		case <-ctx.Done():
			return chunks, ctx.Err()
		default:
		}

		startTime := i * cfg.ChunkDurationSec
		outputPath := filepath.Join(cfg.OutputDir, fmt.Sprintf("chunk_%03d.%s", i, cfg.Format))

		// Always re-encode chunks.
		// "Copy" can produce chunks that start mid-frame and are valid files but fail to decode/transcribe,
		// which looks like a truncated transcript.
		cmd := exec.CommandContext(ctx, p.ffmpegPath,
			"-ss", strconv.Itoa(startTime),
			"-i", audioPath,
			"-t", strconv.Itoa(cfg.ChunkDurationSec),
			"-acodec", getAudioCodec(cfg.Format),
			"-b:a", "64k",
			"-y",
			outputPath,
		)
		if err := cmd.Run(); err != nil {
			continue // Skip failed chunks
		}

		// Verify chunk was created and has content
		if stat, err := os.Stat(outputPath); err == nil && stat.Size() > 1000 {
			chunks = append(chunks, outputPath)
		}
	}

	return chunks, nil
}

// ProcessVideoResult contains all extracted data from a video.
type ProcessVideoResult struct {
	KeyframePaths []string
	AudioPath     string
	AudioDuration float64
	VideoInfo     *VideoInfo
}

// ProcessVideo extracts both keyframes and audio from a video file.
// This is a convenience method that combines ExtractKeyframes and ExtractAudio.
func (p *VideoProcessor) ProcessVideo(ctx context.Context, videoPath, outputDir string) (*ProcessVideoResult, error) {
	result := &ProcessVideoResult{}

	// Get video info
	info, err := p.GetVideoInfo(ctx, videoPath)
	if err != nil {
		return nil, fmt.Errorf("get video info: %w", err)
	}
	result.VideoInfo = info

	// Create subdirectories
	framesDir := filepath.Join(outputDir, "frames")
	audioDir := filepath.Join(outputDir, "audio")

	// Extract keyframes
	frames, err := p.ExtractKeyframes(ctx, videoPath, ExtractKeyframesConfig{
		IntervalSeconds: calculateInterval(info.Duration),
		MaxFrames:       20,
		MaxWidth:        1280,
		Quality:         5,
		OutputDir:       framesDir,
	})
	if err != nil {
		// Non-fatal - continue without frames
		frames = []string{}
	}
	result.KeyframePaths = frames

	// Extract audio if present
	if info.HasAudio {
		audioPath := filepath.Join(audioDir, "audio.mp3")
		path, duration, err := p.ExtractAudio(ctx, videoPath, ExtractAudioConfig{
			OutputPath: audioPath,
			Format:     "mp3",
			SampleRate: 16000,
			Channels:   1,
			Bitrate:    "64k",
		})
		if err == nil {
			result.AudioPath = path
			result.AudioDuration = duration
		}
	}

	return result, nil
}

// calculateInterval determines the frame extraction interval based on video duration.
func calculateInterval(duration float64) int {
	switch {
	case duration < 30:
		return 5 // Short video: every 5 seconds
	case duration < 120:
		return 10 // Medium video: every 10 seconds
	case duration < 600:
		return 30 // Long video: every 30 seconds
	default:
		return 60 // Very long video: every minute
	}
}

// CleanupTempFiles removes temporary files created during processing.
func CleanupTempFiles(paths ...string) {
	for _, path := range paths {
		os.Remove(path)
	}
}

// IsAvailable checks if ffmpeg is available on the system.
func IsAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		return false
	}
	_, err = exec.LookPath("ffprobe")
	return err == nil
}

// GetVersion returns the ffmpeg version string.
func GetVersion() (string, error) {
	cmd := exec.Command("ffmpeg", "-version")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(output), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0]), nil
	}
	return "unknown", nil
}

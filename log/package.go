// Package log defines the logging interface for go-winfsp.
//
// Given that there're many go logging frameworks out there,
// we can't make the choice. So we require user to adapt
// the logger they choose into our logging interface.
//
// On the other hand, we can define a more semantic logging
// interface to specify what topic we are about to log, so
// that user gains more control in processing and filtering
// logs by topics.
package log

// Topics specify the masks of the logger topic.
//
// The logger will query and see if the current logging
// topic has been enabled, so that it won't spend time on
// generating log calls that is not required.
type Topics int

const (
	// TopicCall records the calling arguments and result
	// of a function.
	//
	// This affects `Log.Call` and `Log.Return` interface.
	// They won't be called if TopicCall is not enabled.
	TopicCall Topics = 1 << iota

	// TopicVerdict records choices that are made inside
	// each functions.
	//
	// This affects `Log.Log` and `Log.Logf` when the
	// topics contain TopicVerdict.
	TopicVerdict

	// TopicTrace records the branch traces in each functions.
	//
	// This affects `Log.Log` and `Log.Logf` when the
	// topics contain TopicTrace.
	TopicTrace

	// TopicError records the internal errors that will
	// be remediated in the system.
	//
	// This affects `Log.Log` and `Log.Logf` when the
	// topics contain TopicError.
	TopicError
)

const (
	AllTopics = Topics(0) |
		TopicCall |
		TopicVerdict |
		TopicTrace |
		TopicError
)

// M is the shorthand for `map[string]any`.
type M = map[string]any

// Log is the logger interface.
type Log interface {
	// Check if any of the topic is enabled.
	Enabled(Topics) bool

	// Callf records the calling arguments of a function.
	//
	// The function will need to generate a cookie for call,
	// so that it can be to associate the result.
	Call(name string, args M) string

	// Return records the calling result of a function.
	//
	// The previously generated cookie for call will be used.
	Return(name, cookie string, rets M)

	// Log with the specified topics.
	Log(topics Topics, msg string)

	// Logf with the specified topics.
	Logf(topics Topics, msg string, args ...any)
}

// NoLog is the null implementation of the Log.
type NoLog struct{}

func (NoLog) Enabled(Topics) bool                         { return false }
func (NoLog) Call(string, M) string                       { return "" }
func (NoLog) Log(topics Topics, msg string)               {}
func (NoLog) Logf(topics Topics, msg string, args ...any) {}
func (NoLog) Return(name, cookie string, rets M)          {}

var _ Log = (*NoLog)(nil)

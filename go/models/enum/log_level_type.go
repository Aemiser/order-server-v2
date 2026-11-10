package enum

type LogLevelType int8

const (
	LogInfoLevel = 1
	LogWarnLevel = 2
	LogErrLevel  = 3
)

// 获取日志级别
func (level LogLevelType) String() string {
	switch level {
	case LogInfoLevel:
		return "info"
	case LogWarnLevel:
		return "warn"
	case LogErrLevel:
		return "error"
	}
	return "info"
}

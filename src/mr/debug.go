// Date:   Fri Apr 05 16:29:21 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package mr

import "fmt"

func printColorful(code int, prefix string, format string, args ...interface{}) {
	fmt.Printf("\x1b[%dm%s %s\x1b[0m\n", code, prefix, fmt.Sprintf(format, args...))
}

func Info(format string, args ...interface{}) {
	printColorful(34, "[INFO]", format, args...)
}

func Debug(format string, args ...interface{}) {
	printColorful(33, "[DEBUG]", format, args...)
}

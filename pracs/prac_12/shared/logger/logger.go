package logger

import (
	"os"

	"github.com/sirupsen/logrus"
)

// New creates a logrus logger configured for the given service.
// Output format is JSON with a timestamp field "ts".
func New(service string) *logrus.Entry {
	log := logrus.New()
	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.DebugLevel)
	log.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
		FieldMap: logrus.FieldMap{
			logrus.FieldKeyTime: "ts",
		},
	})

	return log.WithField("service", service)
}

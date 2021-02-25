// Copyright 2019 The Morning Consult, LLC or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//         https://www.apache.org/licenses/LICENSE-2.0
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package toolkit

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"net/smtp"
	"os"
	"strings"

	multierror "github.com/hashicorp/go-multierror"
	"github.com/morningconsult/go-elasticsearch-alerts/command/alert"
	"golang.org/x/xerrors"
)

const (
	// EnvEmailAuthUsername sets the username with which to
	// authenticate to the SMTP server.
	EnvEmailAuthUsername = "GO_ELASTICSEARCH_ALERTS_SMTP_USERNAME"

	// EnvEmailAuthPassword set the password with which to
	// authenticate to the SMTP server.
	EnvEmailAuthPassword = "GO_ELASTICSEARCH_ALERTS_SMTP_PASSWORD"
)

var _ alert.Method = (*AlertMethod)(nil)

// AlertMethodConfig is used to configure where email
// alerts should be sent.
type AlertMethodConfig struct {
	Host     string   `mapstructure:"host"`
	Port     int      `mapstructure:"port"`
	From     string   `mapstructure:"from"`
	To       []string `mapstructure:"to"`
	Username string   `mapstructure:"username"`
	Password string   `mapstructure:"password"`
	Site string       `mapstructure:"site"`
	Environment string `mapstructure:"environment"`
	Team string        `mapstructure:"team"`
	CaseType string    `mapstructure:"caseType"`
	Priority string    `mapstructure:"priority"`
}

// AlertMethod implements the alert.Method interface
// for writing new alerts to email.
type AlertMethod struct {
	host string
	port int
	from string
	auth smtp.Auth
	to   []string
	site string
	environment string
	team string
	caseType string
	priority string
}

// NewAlertMethod creates a new *AlertMethod or a
// non-nil error if there was an error.
func NewAlertMethod(config *AlertMethodConfig) (alert.Method, error) {
	if config == nil {
		return nil, xerrors.New("no config provided")
	}

	if err := validateConfig(config); err != nil {
		return nil, err
	}

	if u := os.Getenv(EnvEmailAuthUsername); u != "" {
		config.Username = u
	}

	if p := os.Getenv(EnvEmailAuthPassword); p != "" {
		config.Password = p
	}

	var auth smtp.Auth
	if config.Username != "" && config.Password != "" {
		auth = smtp.PlainAuth("", config.Username, config.Password, config.Host)
	}

	return &AlertMethod{
		host: config.Host,
		port: config.Port,
		from: config.From,
		to:   config.To,
		auth: auth,
		site: config.Site,
		environment: config.Environment,
		team: config.Team,
		caseType: config.CaseType,
		priority: config.Priority,
	}, nil
}

func validateConfig(config *AlertMethodConfig) error {
	var allErrors *multierror.Error

	if len(config.To) < 1 {
		config.To = []string{"alarms.goto@netcompany.com"}
	}

	if config.Port == 0 {
		config.Port = 25
	}

	if config.Host == "" {
		config.Host = "smtp.nchosting.dk"
	}

	if config.From == "" {
		allErrors = multierror.Append(allErrors, xerrors.New("no sender address provided"))
	}

	return allErrors.ErrorOrNil()
}

// Write creates an email message from the records and sends
// it to the email address(es) specified at the creation of the
// AlertMethod. If there was an error sending the email,
// it returns a non-nil error.
func (e *AlertMethod) Write(ctx context.Context, rule string, records []*alert.Record) error {
	body, err := e.buildMessage(rule, records)
	if err != nil {
		return xerrors.Errorf("error creating email message: %v", err)
	}
	return smtp.SendMail(fmt.Sprintf("%s:%d", e.host, e.port), e.auth, e.from, e.to, []byte(body))
}

// buildMessage creates an email message from the provided
// records. It will return a non-nil error if an error occurs.
func (e *AlertMethod) buildMessage(rule string, records []*alert.Record) (string, error) { // nolint: funlen
	alert := struct {
		Name    string
		Records []*alert.Record
		Site    string
		Environment string
		Team        string
		Priority    string
		CaseType    string
	}{
		rule,
		records,
		e.site,
		e.environment,
		e.team,
		e.priority,
		e.caseType,
	}

	funcs := template.FuncMap{
		"tabsAndLines": func(text string) template.HTML {
			escaped := strings.Replace(template.HTMLEscapeString(text), "\n", "<br>", -1)
			return template.HTML(strings.Replace(escaped, " ", "&nbsp;", -1)) // nolint: gosec
		},
	}

	tpl := `Content-Type: text/html
Subject: {{ .Name }}
{{ if .Site }}@@SITE={{ .Site }}@@{{ end }}{{ if .Environment }} @@Environment={{ .Environment }}@@{{ end }}{{ if .Team }} @@Team={{ .Team }}@@{{ end }}{{ if .Priority }} @@Priority={{ .Priority }}@@{{ end }}{{ if .CaseType }} @@CaseType={{ .CaseType }}@@{{ end }}
<!DOCTYPE html>
<html>
<head>
<style>
table {
    font-family: arial, sans-serif;
    border-collapse: collapse;
}

td, th {
    border: 1px solid #dddddd;
    text-align: left;
    padding: 8px;
}

tr:nth-child(even) {
    background-color: #dddddd;
}
</style>
</head>
<body>
{{ range .Records }}<h4>Filter path: {{ .Filter }}</h4>{{ if .Fields }}
<table>
  <tr>
    <th>Key</th>
    <th>Count</th>
  </tr>{{ range .Fields }}
  <tr>
    <td>{{ .Key }}</td>
    <td>{{ .Count }}</td>
  </tr>{{ end }}
</table>{{ end }}
{{ tabsAndLines .Text }}
<br>{{ end }}
</body>
</html>`
	t, err := template.New("toolkit").Funcs(funcs).Parse(tpl)
	if err != nil {
		return "", xerrors.Errorf("error parsing toolkit template: %v", err)
	}

	buf := &bytes.Buffer{}
	err = t.Execute(buf, alert)
	if err != nil {
		return "", xerrors.Errorf("error executing toolkit template: %v", err)
	}
	return buf.String(), nil
}

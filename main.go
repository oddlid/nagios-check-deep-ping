package main

// Trying to rewrite check_deep_pings_v2.pl in Go
// Odd E. Ebbesen, 2015-09-09 14:04:18

/*
This whole program has been written in a concurrent style with channels, for the ability 
to run several checks in parallell, even though it will not be used that way. This is just 
for the sake of documenting the way of doing this for later checks that might need to fetch
URLs in parallell.
*/

// Sample XML data
/*
<TestReply>
	<System>portal</System>
	<Status Value="Ok"/>
	<Description>Looking good</Description>
</TestReply>
*/

import (
	"crypto/tls"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	log "github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	VERSION    string  = "2015-10-08"
	UA         string  = "VGT Deep Pings/3.0"
	defPort    int     = 80
	defWarn    float64 = 10.0
	defCrit    float64 = 15.0
	defTmout   float64 = 30.0
	defChkStr  string  = "Ok"
	defProt    string  = "http"
	S_OK       string  = "OK"
	S_WARNING  string  = "WARNING"
	S_CRITICAL string  = "CRITICAL"
	S_UNKNOWN  string  = "UNKNOWN"
	E_OK       int     = 0
	E_WARNING  int     = 1
	E_CRITICAL int     = 2
	E_UNKNOWN  int     = 3
)

type DPResponse struct {
	StatusVal   string
	Description string
	RTime       time.Duration
}

func nagios_result(ex_code int, status, desc, path string, rtime, warn, crit float64) {
	fmt.Printf("%s: %s, %s, response time: %f|time=%fs;%fs;%fs\n", status, desc, path, rtime, rtime, warn, crit)
	os.Exit(ex_code)
}

func geturl(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		//log.Fatalf("Unable to create request, error: %s", err)
		nagios_result(E_CRITICAL, S_CRITICAL, "Unable to create request", url, 0, 0, 0)
	}
	req.Header.Set("User-Agent", UA)

	tr := &http.Transport{DisableKeepAlives: true} // we're not reusing the connection, so don't let it hang open
	if strings.Index(url, "https") >= 0 {
		// Verifying certs is not the job of this plugin, so we save ourselves a lot of grief 
		// by skipping any SSL verification
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	client := &http.Client{Transport: tr}

	return client.Do(req)
}

func scrape(url string, chres chan DPResponse, chctrl chan bool) {
	defer func() {
		chctrl <- true // send signal that parsing/scraping is done
	}()

	// We only measure the time to fetch the URL, not included the time to parse
	t_start := time.Now()
	resp, err := geturl(url)
	t_end := time.Now()
	if err != nil {
		//log.Fatalf("Problem retrieving URL, error: %s", err)
		nagios_result(E_CRITICAL, S_CRITICAL, "Connection refused", url, 0, 0, 0)
	}
	doc, err := goquery.NewDocumentFromResponse(resp)
	if err != nil {
		log.Fatalf("Problem loading document, error: %s", err)
	}

	sval, _ := doc.Find("status").Attr("value")
	desc := doc.Find("description").Text()

	// send the results back to caller
	chres <- DPResponse{sval, desc, t_end.Sub(t_start)}
}

func run_check(c *cli.Context) {
	prot := c.String("protocol")
	host := c.String("hostname")
	port := c.Int("port")
	path := c.String("urlpath")
	warn := c.Float64("warning")
	crit := c.Float64("critical")
	tmout := c.Float64("timeout")
	chkstr := c.String("checkstr")

	log.Debugf("Protocol     : %s", prot)
	log.Debugf("Host         : %s", host)
	log.Debugf("Port         : %d", port)
	log.Debugf("UPath        : %s", path)
	log.Debugf("Warning      : %f", warn)
	log.Debugf("Critical     : %f", crit)
	log.Debugf("Timeout      : %f", tmout)
	log.Debugf("Check string : %q", chkstr)

	chResults := make(chan DPResponse)
	chCtrl := make(chan bool)
	defer close(chResults)
	defer close(chCtrl)

	dpurl := fmt.Sprintf("%s://%s:%d%s", prot, host, port, path)
	log.Debugf("DP URL: %s", dpurl)

	// run in parallell thread
	go scrape(dpurl, chResults, chCtrl)

	select {
	case res := <-chResults:
		log.Debugf("Status Value:  %s", res.StatusVal)
		log.Debugf("Description:   %s", res.Description)
		log.Debugf("Response time: %f", res.RTime.Seconds())

		if strings.ToUpper(res.StatusVal) == strings.ToUpper(chkstr) {
			if res.RTime.Seconds() >= warn && res.RTime.Seconds() < crit {
				msg := fmt.Sprintf("Too long response time (>= %ds), (desc: %s)", int(warn), res.Description)
				nagios_result(E_WARNING, S_WARNING, msg, path, res.RTime.Seconds(), warn, crit)
			} else if res.RTime.Seconds() >= crit {
				msg := fmt.Sprintf("Too long response time (>= %ds), (desc: %s)", int(crit), res.Description)
				nagios_result(E_CRITICAL, S_CRITICAL, msg, path, res.RTime.Seconds(), warn, crit)
			} else {
				nagios_result(E_OK, S_OK, res.Description, path, res.RTime.Seconds(), warn, crit)
			}
		} else {
			nagios_result(E_CRITICAL, S_CRITICAL, res.Description, path, res.RTime.Seconds(), warn, crit)
		}
	case <-chCtrl:
		log.Debug("Got done signal. Bye.")
	case <-time.After(time.Second * time.Duration(tmout)):
		fmt.Printf("%s: DP %q timed out after %d seconds.\n", S_CRITICAL, dpurl, int(tmout))
		os.Exit(E_CRITICAL)
	}
}

func main() {
	app := cli.NewApp()
	app.Name = "check_deep_ping"
	app.Version = VERSION
	app.Author = "Odd E. Ebbesen"
	app.Email = "odd.ebbesen@wirelesscar.com"
	app.Usage = "XML Rest API parser for WirelessCar Deep Pings"
	app.EnableBashCompletion = true
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "hostname, H",
			Usage: "Hostname or IP to check",
		},
		cli.IntFlag{
			Name:  "port, p",
			Value: defPort,
			Usage: "TCP port",
		},
		cli.StringFlag{
			Name:  "protocol, P",
			Value: defProt,
			Usage: "Protocol to use (http or https)",
		},
		cli.StringFlag{
			Name:  "urlpath, u",
			Usage: "The path part of the url",
		},
		cli.Float64Flag{
			Name:  "warning, w",
			Value: defWarn,
			Usage: "Response time to result in WARNING status, in seconds",
		},
		cli.Float64Flag{
			Name:  "critical, c",
			Value: defCrit,
			Usage: "Response time to result in CRITICAL status, in seconds",
		},
		cli.Float64Flag{
			Name:  "timeout, t",
			Value: defTmout,
			Usage: "Number of seconds before connection times out",
		},
		cli.StringFlag{
			Name:  "checkstr, s",
			Value: defChkStr,
			Usage: "Search string to check for in the status result",
		},
		cli.StringFlag{
			Name:  "log-level, l",
			Value: "info",
			Usage: "Log level (options: debug, info, warn, error, fatal, panic)",
		},
		cli.BoolFlag{
			Name:   "debug, d",
			Usage:  "Run in debug mode",
			EnvVar: "DEBUG",
		},
	}

	app.Before = func(c *cli.Context) error {
		log.SetOutput(os.Stdout)
		level, err := log.ParseLevel(c.String("log-level"))
		if err != nil {
			log.Fatalf(err.Error())
		}
		log.SetLevel(level)
		if !c.IsSet("log-level") && !c.IsSet("l") && c.Bool("debug") {
			log.SetLevel(log.DebugLevel)
		}
		return nil
	}

	app.Action = run_check
	app.Run(os.Args)
}

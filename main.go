package main

import (
	"bufio"
	"errors"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/fatih/color"
	"github.com/mattn/go-isatty"
	"github.com/mattn/go-runewidth"
	"github.com/pkg/browser"
	"github.com/russross/blackfriday"
	"github.com/shurcooL/github_flavored_markdown"
	"github.com/shurcooL/github_flavored_markdown/gfmstyle"
	"github.com/urfave/cli"
)

const column = 30

const templateDirContent = `
<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <title>Memo Life For You</title>
</head>
<style>
li {list-style-type: none;}
</style>
<body>
<ul>
{{range .}}
  <li><a href="/{{.}}">{{.}}</a></li>
{{end}}
</ul>
</body>
</html>
`

const templateBodyContent = `
<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <title>{{.Name}}</title>
  <link href="/assets/gfm/gfm.css" media="all" rel="stylesheet" type="text/css" />
</head>
<body>
{{.Body}}
</body>
</html>
`

const markdownExtensions = blackfriday.EXTENSION_NO_INTRA_EMPHASIS |
	blackfriday.EXTENSION_TABLES |
	blackfriday.EXTENSION_FENCED_CODE |
	blackfriday.EXTENSION_AUTOLINK |
	blackfriday.EXTENSION_STRIKETHROUGH |
	blackfriday.EXTENSION_HARD_LINE_BREAK |
	blackfriday.EXTENSION_SPACE_HEADERS

type config struct {
	MemoDir   string `toml:"memodir"`
	Editor    string `toml:"editor"`
	Column    int    `toml:"column"`
	SelectCmd string `toml:"selectcmd"`
	GrepCmd   string `toml:"GrepCmd"`
}

var commands = []cli.Command{
	{
		Name:    "new",
		Aliases: []string{"n"},
		Usage:   "create memo",
		Action:  cmdNew,
	},
	{
		Name:    "list",
		Aliases: []string{"l"},
		Usage:   "list memo",
		Action:  cmdList,
	},
	{
		Name:    "edit",
		Aliases: []string{"e"},
		Usage:   "edit memo",
		Action:  cmdEdit,
	},
	{
		Name:    "grep",
		Aliases: []string{"g"},
		Usage:   "grep memo",
		Action:  cmdGrep,
	},
	{
		Name:    "config",
		Aliases: []string{"c"},
		Usage:   "configure",
		Action:  cmdConfig,
	},
	{
		Name:    "serve",
		Aliases: []string{"s"},
		Usage:   "start http server",
		Action:  cmdServe,
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:  "addr",
				Value: ":8080",
				Usage: "server address",
			},
		},
	},
}

func (cfg *config) load() error {
	dir := os.Getenv("HOME")
	if dir == "" && runtime.GOOS == "windows" {
		dir = os.Getenv("APPDATA")
		if dir == "" {
			dir = filepath.Join(os.Getenv("USERPROFILE"), "Application Data", "memo")
		}
		dir = filepath.Join(dir, "memo")
	} else {
		dir = filepath.Join(dir, ".config", "memo")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file := filepath.Join(dir, "config.toml")

	_, err := os.Stat(file)
	if err == nil {
		_, err := toml.DecodeFile(file, cfg)
		return err
	}
	if !os.IsNotExist(err) {
		return err
	}
	f, err := os.Create(file)
	if err != nil {
		return err
	}

	dir = filepath.Join(dir, "_posts")
	os.MkdirAll(dir, 0700)
	cfg.MemoDir = filepath.ToSlash(dir)
	cfg.Editor = "vim"
	cfg.Column = 20
	cfg.SelectCmd = "peco"
	cfg.GrepCmd = "grep"
	return toml.NewEncoder(f).Encode(cfg)
}

func msg(err error) int {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", os.Args[0], err)
		return 1
	}
	return 0
}

func filterMarkdown(files []string) []string {
	var newfiles []string
	for _, file := range files {
		if strings.HasSuffix(file, ".md") {
			newfiles = append(newfiles, file)
		}
	}
	sort.Strings(newfiles)
	return newfiles
}

func run() int {
	var cfg config
	err := cfg.load()
	if err != nil {
		return msg(err)
	}

	app := cli.NewApp()
	app.Name = "memo"
	app.Usage = "Memo Life For You"
	app.Version = "0.0.1"
	app.Commands = commands
	app.Metadata = map[string]interface{}{"config": &cfg}
	return msg(app.Run(os.Args))
}

func firstline(name string) string {
	f, err := os.Open(name)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return ""
	}
	if scanner.Err() != nil {
		return ""
	}
	return strings.TrimLeft(scanner.Text(), "# ")
}

func cmdList(c *cli.Context) error {
	cfg := c.App.Metadata["config"].(*config)
	f, err := os.Open(cfg.MemoDir)
	if err != nil {
		return err
	}
	defer f.Close()
	files, err := f.Readdirnames(-1)
	if err != nil {
		return err
	}
	files = filterMarkdown(files)
	istty := isatty.IsTerminal(os.Stdout.Fd())
	col := cfg.Column
	if col == 0 {
		col = column
	}
	pat := c.Args().First()
	for _, file := range files {
		if pat != "" && !strings.Contains(file, pat) {
			continue
		}
		if istty {
			title := runewidth.Truncate(firstline(filepath.Join(cfg.MemoDir, file)), 80-col, "...")
			file = runewidth.FillRight(runewidth.Truncate(file, col, "..."), col)
			fmt.Fprintf(color.Output, "%s : %s\n", color.GreenString(file), color.YellowString(title))
		} else {
			fmt.Println(file)
		}
	}
	return nil
}

func escape(name string) string {
	s := regexp.MustCompile(`[ <>:"/\\|?*%#]`).ReplaceAllString(name, "-")
	s = regexp.MustCompile(`--+`).ReplaceAllString(s, "-")
	return strings.Trim(strings.Replace(s, "--", "-", -1), "- ")
}

func edit(editor string, files ...string) error {
	cmd := exec.Command(editor, files...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func cmdNew(c *cli.Context) error {
	cfg := c.App.Metadata["config"].(*config)

	var file string
	if c.Args().Present() {
		file = c.Args().First()
	} else {
		fmt.Print("Title: ")
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			return errors.New("canceld")
		}
		if scanner.Err() != nil {
			return scanner.Err()
		}
		file = scanner.Text()
	}
	file = time.Now().Format("2006-01-02-") + escape(file) + ".md"
	file = filepath.Join(cfg.MemoDir, file)
	return edit(cfg.Editor, file)
}

func cmdEdit(c *cli.Context) error {
	cfg := c.App.Metadata["config"].(*config)

	var files []string
	if c.Args().Present() {
		files = append(files, filepath.Join(cfg.MemoDir, c.Args().First()))
	} else {
		f, err := os.Open(cfg.MemoDir)
		if err != nil {
			return err
		}
		defer f.Close()
		files, err = f.Readdirnames(-1)
		if err != nil {
			return err
		}
		files = filterMarkdown(files)
		cmd := exec.Command(cfg.SelectCmd)
		cmd.Stdin = strings.NewReader(strings.Join(files, "\n"))
		b, err := cmd.CombinedOutput()
		if err != nil {
			return err
		}
		files = strings.Split(strings.TrimSpace(string(b)), "\n")
		for i, file := range files {
			files[i] = filepath.Join(cfg.MemoDir, file)
		}
	}
	return edit(cfg.Editor, files...)
}

func cmdGrep(c *cli.Context) error {
	cfg := c.App.Metadata["config"].(*config)

	if !c.Args().Present() {
		return nil
	}
	f, err := os.Open(cfg.MemoDir)
	if err != nil {
		return err
	}
	defer f.Close()
	files, err := f.Readdirnames(-1)
	if err != nil {
		return err
	}
	files = filterMarkdown(files)
	var args []string
	args = append(args, c.Args().First())
	for _, file := range files {
		args = append(args, filepath.Join(cfg.MemoDir, file))
	}
	cmd := exec.Command(cfg.GrepCmd, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func cmdConfig(c *cli.Context) error {
	cfg := c.App.Metadata["config"].(*config)

	dir := os.Getenv("HOME")
	if dir == "" && runtime.GOOS == "windows" {
		dir = os.Getenv("APPDATA")
		if dir == "" {
			dir = filepath.Join(os.Getenv("USERPROFILE"), "Application Data", "memo")
		}
		dir = filepath.Join(dir, "memo")
	} else {
		dir = filepath.Join(dir, ".config", "memo")
	}
	file := filepath.Join(dir, "config.toml")
	return edit(cfg.Editor, file)
}

func cmdServe(c *cli.Context) error {
	cfg := c.App.Metadata["config"].(*config)

	http.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/" {
			f, err := os.Open(cfg.MemoDir)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer f.Close()
			files, err := f.Readdirnames(-1)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			files = filterMarkdown(files)
			w.Header().Set("content-type", "text/html")
			t := template.Must(template.New("dir").Parse(templateDirContent))
			err = t.Execute(w, files)
			if err != nil {
				log.Println(err)
			}
		} else {
			p := filepath.Join(cfg.MemoDir, escape(req.URL.Path))
			b, err := ioutil.ReadFile(p)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			body := string(b)
			if strings.HasPrefix(body, "---\n") {
				if pos := strings.Index(body[4:], "---\n"); pos > 0 {
					body = body[4+pos+4:]
				}
			}
			//renderer := blackfriday.HtmlRenderer(0, "", "")
			//body = string(blackfriday.Markdown([]byte(body), renderer, markdownExtensions))
			body = string(github_flavored_markdown.Markdown([]byte(body)))
			t := template.Must(template.New("body").Parse(templateBodyContent))
			t.Execute(w, struct {
				Name string
				Body template.HTML
			}{
				Name: req.URL.Path,
				Body: template.HTML(body),
			})
		}
	})
	http.Handle("/assets/gfm/", http.StripPrefix("/assets/gfm", http.FileServer(gfmstyle.Assets)))

	addr := c.String("addr")
	var url string
	if strings.HasPrefix(addr, ":") {
		url = "http://localhost" + addr
	} else {
		url = "http://" + addr
	}
	browser.OpenURL(url)
	return http.ListenAndServe(addr, nil)
}

func main() {
	os.Exit(run())
}

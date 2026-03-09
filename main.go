package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/zyxar/argo/rpc"
)

const (
	configFile    = "config.json"
	linksFile     = "downloads.list"
	cacheFile     = "shadow_cache.json"
	rpcURL        = "http://127.0.0.1:6800/jsonrpc"
	ColorPhosphor = "#00FF44"
	ColorSeed     = "#00AAFF"
	ColorWhite    = "#FFFFFF"
	ColorError    = "#FF0044"
)

type Config struct {
	AllowedFolders []string `json:"allowed_folders"`
}

type downloadItem struct {
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	GID       string   `json:"gid"`
	DLSpeed   string   `json:"dl_speed"`
	ULSpeed   string   `json:"ul_speed"`
	Peers     string   `json:"peers"`
	Dir       string   `json:"dir"`
	ErrorCode string   `json:"error_code"`
	Progress  float64  `json:"progress"`
	URIs      []string `json:"uris,omitempty"`
}

type syncMsg struct {
	Tasks     map[string]downloadItem
	Order     []string
	RPCActive bool
}

type commandMsg struct{ err error }

type model struct {
	tasks     map[string]downloadItem
	taskOrder []string
	cursor    int
	input     textinput.Model
	folders   []string
	folderIdx int
	aria      rpc.Client
	spinIdx   int
	rpcActive bool
}

// МГНОВЕННЫЙ ЗАПУСК ИЗ КЭША
func loadInitialState() (map[string]downloadItem, []string) {
	mMap := make(map[string]downloadItem)
	var order []string
	data, err := os.ReadFile(cacheFile)
	if err == nil {
		var cached map[string]downloadItem
		if err := json.Unmarshal(data, &cached); err == nil {
			for gid, item := range cached {
				if item.Name == "" || strings.Contains(item.Name, "[METADATA]") { continue }
				mMap[gid] = downloadItem{
					Name: item.Name, Status: "waits", GID: gid, Dir: item.Dir,
					DLSpeed: "0.0", ULSpeed: "0.0", Peers: "0",
				}
				order = append(order, gid)
			}
		}
	}
	return mMap, order
}

func (m model) saveSnapshot() {
	data, _ := json.MarshalIndent(m.tasks, "", "  ")
	_ = os.WriteFile(cacheFile, data, 0644)
}

func extractName(uri string) string {
	if strings.HasPrefix(uri, "magnet:") {
		u, err := url.Parse(uri)
		if err == nil {
			if n := u.Query().Get("dn"); n != "" { return n }
		}
		return "Magnet (fetching...)"
	}
	return filepath.Base(uri)
}

func (m model) Init() tea.Cmd { return m.tick() }

func (m model) tick() tea.Cmd {
	return tea.Tick(600*time.Millisecond, func(t time.Time) tea.Msg { return m.fetchAriaData() })
}

func (m model) fetchAriaData() tea.Msg {
	a, err1 := m.aria.TellActive()
	w, err2 := m.aria.TellWaiting(0, 1000)
	s, err3 := m.aria.TellStopped(0, 1000)
	rpcOK := (err1 == nil && err2 == nil && err3 == nil)
	
	newTasks := make(map[string]downloadItem)
	for k, v := range m.tasks { newTasks[k] = v } // Базируемся на текущем кэше
	
	activeGids := make(map[string]bool)
	var liveOrder []string
	seenNames := make(map[string]bool)

	if rpcOK {
		all := append(append(a, w...), s...)
		for _, t := range all {
			name := t.BitTorrent.Info.Name
			if name == "" && len(t.Files) > 0 { name = filepath.Base(t.Files[0].Path) }
			if strings.Contains(name, "[METADATA]") || name == "" { 
				// Если имени нет в ответе, пробуем оставить имя из кэша!
				if cached, ok := m.tasks[t.Gid]; ok && cached.Name != "" {
					name = cached.Name
				} else { continue }
			}

			c, _ := strconv.ParseFloat(t.CompletedLength, 64)
			tot, _ := strconv.ParseFloat(t.TotalLength, 64)
			prog := 0.0; if tot > 0 { prog = c / tot }
			
			status := t.Status
			if status == "active" && prog >= 1.0 { status = "seed" }
			peers := t.Connections
			if t.NumSeeders != "" { peers = fmt.Sprintf("%s/%s", t.Connections, t.NumSeeders) }

			newTasks[t.Gid] = downloadItem{
				Name: name, Status: status, GID: t.Gid, Progress: prog,
				DLSpeed: fmt.Sprintf("%.1f", parseSpeed(t.DownloadSpeed)),
				ULSpeed: fmt.Sprintf("%.1f", parseSpeed(t.UploadSpeed)),
				Peers: peers, Dir: t.Dir, ErrorCode: t.ErrorCode,
			}
			activeGids[t.Gid] = true
			liveOrder = append(liveOrder, t.Gid)
			seenNames[name] = true
		}
	}

	var finalOrder []string
	finalOrder = append(finalOrder, liveOrder...)
	for _, gid := range m.taskOrder {
		if !activeGids[gid] {
			if it, ok := m.tasks[gid]; ok && it.Name != "" && it.Status == "waits" && !seenNames[it.Name] {
				finalOrder = append(finalOrder, gid)
			}
		}
	}
	return syncMsg{Tasks: newTasks, Order: finalOrder, RPCActive: rpcOK}
}

func parseSpeed(s string) float64 {
	b, _ := strconv.ParseFloat(s, 64); return b / (1024 * 1024)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.spinIdx = (m.spinIdx + 1) % 4
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "f10": return m, tea.Quit
		case "up": if m.cursor > 0 { m.cursor-- }
		case "down": if m.cursor < len(m.taskOrder)-1 { m.cursor++ }
		case "tab": m.folderIdx = (m.folderIdx + 1) % len(m.folders)
		case "f4":
			ed := "micro"; if _, err := exec.LookPath("micro"); err != nil { ed = "nano" }
			return m, tea.ExecProcess(exec.Command(ed, linksFile), func(err error) tea.Msg { return commandMsg{err} })
		case "f5":
			if m.cursor < len(m.taskOrder) {
				gid := m.taskOrder[m.cursor]
				m.aria.Remove(gid); m.aria.RemoveDownloadResult(gid)
				delete(m.tasks, gid); m.saveSnapshot()
			}
		case "enter":
			val := strings.TrimSpace(m.input.Value())
			if val != "" {
				dir := m.folders[m.folderIdx]
				gid, _ := m.aria.AddURI([]string{val}, map[string]interface{}{"dir": dir})
				m.tasks[gid] = downloadItem{Name: extractName(val), Status: "waits", GID: gid, Dir: dir}
				m.taskOrder = append([]string{gid}, m.taskOrder...)
				m.saveSnapshot()
				m.input.SetValue(""); return m, nil
			}
		}
	case syncMsg:
		m.tasks = msg.Tasks; m.taskOrder = msg.Order; m.rpcActive = msg.RPCActive
		m.saveSnapshot()
		return m, m.tick()
	case commandMsg:
		return m, tea.Batch(tea.ClearScreen, func() tea.Msg {
			f, _ := os.Open(linksFile); defer f.Close()
			sc := bufio.NewScanner(f); dir := m.folders[m.folderIdx]
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line != "" && !strings.HasPrefix(line, "#") {
					gid, _ := m.aria.AddURI([]string{line}, map[string]interface{}{"dir": dir})
					m.tasks[gid] = downloadItem{Name: extractName(line), Status: "waits", GID: gid, Dir: dir}
					m.taskOrder = append(m.taskOrder, gid)
				}
			}
			m.saveSnapshot()
			return m.fetchAriaData()
		})
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) View() string {
	var b strings.Builder // strings.Builder работает быстрее конкатенации
	phos := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorPhosphor))
	white := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorWhite))
	errSt := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorError)).Bold(true)
	seedSt := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorSeed)).Bold(true)
	waitSt := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555")).Italic(true)
	spin := []string{"-", "\\", "|", "/"}[m.spinIdx]
	div := phos.Render(strings.Repeat("━", 100))

	statusText := phos.Render("● RPC: OK")
	if !m.rpcActive { statusText = errSt.Render("○ RPC: ERR") }

	header := fmt.Sprintf(" ARIA2 FLUX v6.2 (DEEP STATIC) %48s", statusText)
	b.WriteString(phos.Bold(true).Render(header) + "\n" + div + "\n\n")
	
	for i, gid := range m.taskOrder {
		dl, ok := m.tasks[gid]
		if !ok || dl.Name == "" { continue }
		cursor := "  "; if m.cursor == i { cursor = "▶ " }
		stT := strings.ToUpper(dl.Status)
		if dl.ErrorCode != "" && dl.ErrorCode != "0" { stT = "ERR:" + dl.ErrorCode }
		
		stStyle := phos
		if stT == "SEED" { stStyle = seedSt }
		if dl.Status == "waits" { stStyle = waitSt; stT = "WAITS " + spin }
		
		line := fmt.Sprintf("%s%s %-40s | ↓ %6s | ↑ %5s (%-3s) | %6.1f%%\n", 
			cursor, stStyle.Render(fmt.Sprintf("%-12s", "["+stT+"]")), 
			truncate(dl.Name, 40), dl.DLSpeed, dl.ULSpeed, dl.Peers, dl.Progress*100)
		b.WriteString(line)
		
		if m.cursor == i { b.WriteString(waitSt.Render(fmt.Sprintf("    ┗━📂 %s", truncate(dl.Dir, 80))) + "\n") }
	}

	if len(m.taskOrder) == 0 { b.WriteString("  " + white.Render("[ Список пуст ]") + "\n") }
	b.WriteString("\n" + div + "\n" + white.Render("📂 ПАПКА: ") + phos.Render(m.folders[m.folderIdx]) + "\n")
	b.WriteString(white.Render("🔗 ") + m.input.View() + "\n\n")
	b.WriteString(phos.Render(fmt.Sprintf("[ Всего задач: %d ]", len(m.taskOrder))))
	return b.String()
}

func truncate(s string, n int) string { if len(s) <= n { return s }; return s[:n-3] + "..." }

func main() {
	client, _ := rpc.New(context.Background(), rpcURL, "", time.Second*5, nil)
	ti := textinput.New(); ti.Placeholder = "URL..."; ti.Focus()
	tasks, order := loadInitialState()
	m := model{aria: client, input: ti, folders: loadFolders(), tasks: tasks, taskOrder: order}
	tea.NewProgram(m).Run()
}

func loadFolders() []string {
	data, err := os.ReadFile(configFile)
	if err == nil {
		var conf Config; json.Unmarshal(data, &conf)
		if len(conf.AllowedFolders) > 0 { return conf.AllowedFolders }
	}
	return []string{"/home/live/TOR"}
}
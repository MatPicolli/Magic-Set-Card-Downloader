package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Estilos globais
var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FAFAFA")).Background(lipgloss.Color("#7D56F4")).Padding(0, 1)
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5555"))
	successStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#50FA7B"))
	infoStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#8BE9FD"))
	warningStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB86C"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF79C6"))
)

// API structs
type SetData struct {
	Data []Set `json:"data"`
}
type Set struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	SearchURI  string `json:"search_uri"`
	SetType    string `json:"set_type"`
	CardCount  int    `json:"card_count"`
	ReleasedAt string `json:"released_at"`
	Digital    bool   `json:"digital"`
}

type CardData struct {
	Data []Card `json:"data"`
}
type Card struct {
	Name            string            `json:"name"`
	Layout          string            `json:"layout"`
	ImageURIs       map[string]string `json:"image_uris"`
	CardFaces       []CardFace        `json:"card_faces"`
	Set             string            `json:"set"`
	PrintsSearchURI string            `json:"prints_search_uri"`
}
type CardFace struct {
	Name      string            `json:"name"`
	ImageURIs map[string]string `json:"image_uris"`
}

// Messages
type setListMsg []Set
type downloadCompleteMsg struct {
	success           bool
	message           string
	completed, failed []string
}
type errorMsg struct{ err error }
type progressUpdateMsg struct {
	current, total int
	message        string
}

// Estados
type state int

const (
	menuState state = iota
	setListState
	setSearchState
	setDownloadState
	cardDownloadState
	configState
)

// List item
type setItem struct{ set Set }

func (s setItem) FilterValue() string { return s.set.Code + " " + s.set.Name + " " + s.set.SetType }
func (s setItem) Title() string {
	digital := ""
	if s.set.Digital {
		digital = " 💻"
	}
	return fmt.Sprintf("%s - %s%s", strings.ToUpper(s.set.Code), s.set.Name, digital)
}
func (s setItem) Description() string {
	return fmt.Sprintf("%s | %d cartas | %s", s.set.SetType, s.set.CardCount, s.set.ReleasedAt)
}

// Model principal
type model struct {
	state          state
	spinner        spinner.Model
	textInput      textinput.Model
	searchInput    textinput.Model
	progress       progress.Model
	setList        list.Model
	sets           []Set
	currentMenu    int
	menuOptions    []string
	downloadDir    string
	quality        string
	maxWorkers     int
	logs           []string
	downloader     *Downloader
	totalTasks     int64
	completedTasks int64
}

type Downloader struct {
	client      *http.Client
	maxWorkers  int
	downloadDir string
	quality     string
}

func NewDownloader(maxWorkers int, downloadDir, quality string) *Downloader {
	return &Downloader{
		client:      &http.Client{Timeout: 30 * time.Second},
		maxWorkers:  maxWorkers,
		downloadDir: downloadDir,
		quality:     quality,
	}
}

func (m *model) updateDownloaderConfig() {
	m.downloader.downloadDir = m.downloadDir
	m.downloader.quality = m.quality
	m.downloader.maxWorkers = m.maxWorkers
}

func (d *Downloader) fetchSets() ([]Set, error) {
	resp, err := d.client.Get("https://api.scryfall.com/sets")
	if err != nil {
		return nil, fmt.Errorf("erro ao buscar sets: %w", err)
	}
	defer resp.Body.Close()

	var setData SetData
	if err := json.NewDecoder(resp.Body).Decode(&setData); err != nil {
		return nil, fmt.Errorf("erro ao decodificar sets: %w", err)
	}

	sort.Slice(setData.Data, func(i, j int) bool { return setData.Data[i].ReleasedAt > setData.Data[j].ReleasedAt })
	return setData.Data, nil
}

func (d *Downloader) fetchSetCards(searchURI string) ([]Card, error) {
	allCards := []Card{}
	currentURL := searchURI

	// Loop para pegar todas as páginas
	for currentURL != "" {
		resp, err := d.client.Get(currentURL)
		if err != nil {
			return nil, fmt.Errorf("erro ao buscar cartas do set: %w", err)
		}

		var result struct {
			Data     []Card `json:"data"`
			HasMore  bool   `json:"has_more"`
			NextPage string `json:"next_page"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("erro ao decodificar cartas: %w", err)
		}
		resp.Body.Close()

		// Adicionar cartas desta página
		allCards = append(allCards, result.Data...)

		// Verificar se há mais páginas
		if result.HasMore && result.NextPage != "" {
			currentURL = result.NextPage
			// Pequena pausa para não sobrecarregar a API
			time.Sleep(100 * time.Millisecond)
		} else {
			currentURL = ""
		}
	}

	return allCards, nil
}

func (d *Downloader) fetchCard(cardName string) (*Card, error) {
	cardName = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(cardName, " ", "+"), "/", "+"), ",", "+"), "'", "")
	url := fmt.Sprintf("https://api.scryfall.com/cards/named?fuzzy=%s", cardName)
	resp, err := d.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("erro ao buscar carta: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("carta não encontrada")
	}

	var card Card
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		return nil, fmt.Errorf("erro ao decodificar carta: %w", err)
	}
	return &card, nil
}

func (d *Downloader) downloadImage(url, fileName, setCode string) error {
	setDir := filepath.Join(d.downloadDir, strings.ToUpper(setCode))
	if err := os.MkdirAll(setDir, 0755); err != nil {
		return fmt.Errorf("erro ao criar diretório %s: %w", setDir, err)
	}

	// Limpeza mais robusta de caracteres inválidos
	fileName = strings.TrimSpace(fileName)
	invalidChars := []string{":", "?", "\"", "*", "<", ">", "|", "/", "\\"}
	for _, char := range invalidChars {
		fileName = strings.ReplaceAll(fileName, char, "")
	}

	// Remover espaços duplos e caracteres especiais adicionais
	fileName = strings.ReplaceAll(fileName, "  ", " ")
	fileName = strings.ReplaceAll(fileName, "'", "")
	fileName = strings.ReplaceAll(fileName, ",", "")

	filePath := filepath.Join(setDir, fileName+".full.jpg")

	if _, err := os.Stat(filePath); err == nil {
		return nil // Já existe
	}

	resp, err := d.client.Get(url)
	if err != nil {
		return fmt.Errorf("erro ao baixar %s: %w", fileName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("falha no download %s: HTTP %d", fileName, resp.StatusCode)
	}

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("erro ao criar arquivo %s: %w", filePath, err)
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	return err
}

func (d *Downloader) processCard(card Card) []func() error {
	var tasks []func() error

	// Função helper para adicionar task de download
	addDownloadTask := func(imageURL, cardName string) {
		if imageURL != "" {
			tasks = append(tasks, func() error {
				return d.downloadImage(imageURL, cardName, card.Set)
			})
		}
	}

	// Função para tentar diferentes qualidades
	tryDownloadWithFallback := func(imageURIs map[string]string, cardName string) {
		qualities := []string{d.quality, "normal", "small", "large"} // Tenta a qualidade configurada primeiro, depois fallbacks

		for _, quality := range qualities {
			if imageURL, ok := imageURIs[quality]; ok {
				addDownloadTask(imageURL, cardName)
				return // Para no primeiro que encontrar
			}
		}
	}

	switch card.Layout {
	case "adventure":
		// Cartas Adventure (uma face principal)
		name := strings.Split(card.Name, " //")[0]
		tryDownloadWithFallback(card.ImageURIs, name)

	case "transform", "modal_dfc", "reversible_card", "double_faced_token":
		// Cartas de dupla face
		for _, face := range card.CardFaces {
			if len(face.ImageURIs) > 0 {
				tryDownloadWithFallback(face.ImageURIs, face.Name)
			}
		}

	case "split", "flip":
		// Cartas split/flip - geralmente uma imagem só
		if len(card.ImageURIs) > 0 {
			tryDownloadWithFallback(card.ImageURIs, card.Name)
		} else {
			// Fallback para faces individuais se necessário
			for _, face := range card.CardFaces {
				if len(face.ImageURIs) > 0 {
					tryDownloadWithFallback(face.ImageURIs, face.Name)
				}
			}
		}

	default:
		// Cartas normais e outros layouts
		if strings.Contains(card.Name, "//") && len(card.CardFaces) > 0 {
			// Tem faces separadas
			for _, face := range card.CardFaces {
				if len(face.ImageURIs) > 0 {
					tryDownloadWithFallback(face.ImageURIs, face.Name)
				}
			}
		} else {
			// Carta normal
			if len(card.ImageURIs) > 0 {
				tryDownloadWithFallback(card.ImageURIs, card.Name)
			}
		}
	}

	return tasks
}

func initialModel() model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	ti := textinput.New()
	ti.Placeholder = "Digite aqui..."
	ti.CharLimit = 200
	ti.Width = 60

	si := textinput.New()
	si.Placeholder = "Buscar sets..."
	si.CharLimit = 50
	si.Width = 40

	prog := progress.New(progress.WithDefaultGradient())

	setList := list.New([]list.Item{}, list.NewDefaultDelegate(), 70, 20)
	setList.Title = "Sets Disponíveis"
	setList.SetShowStatusBar(true)
	setList.SetFilteringEnabled(true)

	return model{
		state:       menuState,
		spinner:     s,
		textInput:   ti,
		searchInput: si,
		progress:    prog,
		setList:     setList,
		currentMenu: 0,
		menuOptions: []string{"🎴 Download por Set", "🃏 Download por Carta", "📋 Listar/Buscar Sets", "⚙️ Configurações", "🚪 Sair"},
		downloadDir: "./downloads",
		quality:     "large",
		maxWorkers:  10,
		logs:        []string{},
		downloader:  NewDownloader(10, "./downloads", "large"),
	}
}

func (m model) Init() tea.Cmd { return m.spinner.Tick }

func (m model) tickProgress() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		current := atomic.LoadInt64(&m.completedTasks)
		total := atomic.LoadInt64(&m.totalTasks)
		return progressUpdateMsg{current: int(current), total: int(total), message: fmt.Sprintf("Baixando... %d/%d", current, total)}
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.state {
		case menuState:
			switch msg.String() {
			case "up", "k":
				if m.currentMenu > 0 {
					m.currentMenu--
				}
			case "down", "j":
				if m.currentMenu < len(m.menuOptions)-1 {
					m.currentMenu++
				}
			case "enter":
				switch m.currentMenu {
				case 0: // Download por Set
					m.state = setDownloadState
					m.textInput.SetValue("")
					m.textInput.Placeholder = "Códigos dos sets separados por vírgula (ex: dom,war,m21) ou 'ALL' para todos"
					m.textInput.Focus()
					if len(m.sets) == 0 {
						return m, m.fetchSetsCmd()
					}
				case 1: // Download por Carta
					m.state = cardDownloadState
					m.textInput.SetValue("")
					m.textInput.Placeholder = "Nome da carta (ex: Lightning Bolt)"
					m.textInput.Focus()
				case 2: // Listar Sets
					m.state = setSearchState
					m.searchInput.SetValue("")
					m.searchInput.Focus()
					if len(m.sets) == 0 {
						return m, m.fetchSetsCmd()
					} else {
						m.updateSetList("")
					}
				case 3: // Configurações
					m.state = configState
					m.currentMenu = 0
				case 4: // Sair
					return m, tea.Quit
				}
			case "q", "ctrl+c":
				return m, tea.Quit
			}

		case setSearchState:
			switch msg.String() {
			case "esc":
				m.state = menuState
				m.searchInput.Blur()
			default:
				var cmd tea.Cmd
				m.searchInput, cmd = m.searchInput.Update(msg)
				if msg.String() != "enter" {
					m.updateSetList(m.searchInput.Value())
				}
				var listCmd tea.Cmd
				m.setList, listCmd = m.setList.Update(msg)
				return m, tea.Batch(cmd, listCmd)
			}

		case setDownloadState:
			switch msg.String() {
			case "enter":
				input := strings.TrimSpace(m.textInput.Value())
				if input != "" {
					m.state = setListState
					m.logs = []string{}
					atomic.StoreInt64(&m.totalTasks, 0)
					atomic.StoreInt64(&m.completedTasks, 0)

					if strings.ToUpper(input) == "ALL" {
						var allCodes []string
						for _, set := range m.sets {
							if !set.Digital {
								allCodes = append(allCodes, set.Code)
							}
						}
						m.logs = append(m.logs, fmt.Sprintf("🚀 Iniciando download de TODOS os sets (%d sets)", len(allCodes)))
						return m, tea.Batch(m.spinner.Tick, m.tickProgress(), m.downloadMultipleSetsCmd(allCodes))
					} else {
						codes := strings.Split(input, ",")
						var cleanCodes []string
						for _, code := range codes {
							if clean := strings.TrimSpace(code); clean != "" {
								cleanCodes = append(cleanCodes, clean)
							}
						}
						m.logs = append(m.logs, fmt.Sprintf("🚀 Iniciando download de %d sets: %s", len(cleanCodes), strings.Join(cleanCodes, ", ")))
						return m, tea.Batch(m.spinner.Tick, m.tickProgress(), m.downloadMultipleSetsCmd(cleanCodes))
					}
				}
			case "esc":
				m.state = menuState
				m.textInput.Blur()
			default:
				var cmd tea.Cmd
				m.textInput, cmd = m.textInput.Update(msg)
				return m, cmd
			}

		case cardDownloadState:
			switch msg.String() {
			case "enter":
				if cardName := strings.TrimSpace(m.textInput.Value()); cardName != "" {
					m.state = setListState
					m.logs = []string{}
					atomic.StoreInt64(&m.totalTasks, 0)
					atomic.StoreInt64(&m.completedTasks, 0)
					return m, tea.Batch(m.spinner.Tick, m.tickProgress(), m.downloadCardCmd(cardName))
				}
			case "esc":
				m.state = menuState
				m.textInput.Blur()
			default:
				var cmd tea.Cmd
				m.textInput, cmd = m.textInput.Update(msg)
				return m, cmd
			}

		case setListState:
			if msg.String() == "esc" || msg.String() == "q" {
				m.state = menuState
			}

		case configState:
			switch msg.String() {
			case "esc":
				if m.textInput.Focused() {
					m.textInput.Blur()
				} else {
					m.state = menuState
				}
			case "up", "k":
				if !m.textInput.Focused() && m.currentMenu > 0 {
					m.currentMenu--
				}
			case "down", "j":
				if !m.textInput.Focused() && m.currentMenu < 3 {
					m.currentMenu++
				}
			case "enter":
				if m.textInput.Focused() {
					// Processar o valor inserido
					value := strings.TrimSpace(m.textInput.Value())
					switch m.currentMenu {
					case 0: // Pasta
						if value != "" {
							if err := os.MkdirAll(value, 0755); err != nil {
								m.logs = append(m.logs, errorStyle.Render(fmt.Sprintf("❌ Erro ao acessar pasta: %v", err)))
							} else {
								m.downloadDir = value
								m.updateDownloaderConfig()
								m.logs = append(m.logs, successStyle.Render(fmt.Sprintf("✅ Pasta alterada para: %s", value)))
							}
						}
					case 2: // Workers
						if workers, err := strconv.Atoi(value); err == nil && workers > 0 && workers <= 50 {
							m.maxWorkers = workers
							m.updateDownloaderConfig()
							m.logs = append(m.logs, successStyle.Render(fmt.Sprintf("✅ Workers alterados para: %d", workers)))
						} else {
							m.logs = append(m.logs, errorStyle.Render("⚠️ Número de workers deve ser entre 1 e 50"))
						}
					}
					m.textInput.Blur()
					// Limitar logs
					if len(m.logs) > 10 {
						m.logs = m.logs[len(m.logs)-10:]
					}
				} else {
					// Não está editando, então iniciar edição
					switch m.currentMenu {
					case 0: // Pasta de download
						m.textInput.SetValue(m.downloadDir)
						m.textInput.Placeholder = "Caminho da pasta (ex: C:\\MinhasCartas)"
						m.textInput.Focus()
					case 1: // Qualidade
						qualities := []string{"small", "normal", "large"}
						currentIndex := 0
						for i, q := range qualities {
							if q == m.quality {
								currentIndex = i
								break
							}
						}
						nextIndex := (currentIndex + 1) % len(qualities)
						m.quality = qualities[nextIndex]
						m.updateDownloaderConfig()
						m.logs = append(m.logs, successStyle.Render(fmt.Sprintf("✅ Qualidade alterada para: %s", m.quality)))
						if len(m.logs) > 10 {
							m.logs = m.logs[len(m.logs)-10:]
						}
					case 2: // Workers
						m.textInput.SetValue(strconv.Itoa(m.maxWorkers))
						m.textInput.Placeholder = "Número de workers (1-50)"
						m.textInput.Focus()
					case 3: // Voltar
						m.state = menuState
					}
				}
			default:
				if m.textInput.Focused() {
					var cmd tea.Cmd
					m.textInput, cmd = m.textInput.Update(msg)
					return m, cmd
				}
			}
		}

	case setListMsg:
		m.sets = []Set(msg)
		if m.state == setSearchState {
			m.updateSetList(m.searchInput.Value())
		}

	case progressUpdateMsg:
		if msg.total > 0 {
			progress := float64(msg.current) / float64(msg.total)
			cmd := m.progress.SetPercent(progress)
			return m, tea.Batch(cmd, m.tickProgress())
		}
		return m, m.tickProgress()

	case downloadCompleteMsg:
		m.logs = append(m.logs, "", successStyle.Render("🎉 DOWNLOAD COMPLETO!"), msg.message)
		if len(msg.completed) > 0 {
			m.logs = append(m.logs, successStyle.Render(fmt.Sprintf("✅ Sets baixados com sucesso (%d):", len(msg.completed))))
			for _, code := range msg.completed {
				m.logs = append(m.logs, successStyle.Render(fmt.Sprintf("  ✓ %s", strings.ToUpper(code))))
			}
		}
		if len(msg.failed) > 0 {
			m.logs = append(m.logs, errorStyle.Render(fmt.Sprintf("❌ Sets com falha (%d):", len(msg.failed))))
			for _, code := range msg.failed {
				m.logs = append(m.logs, errorStyle.Render(fmt.Sprintf("  ✗ %s", strings.ToUpper(code))))
			}
		}
		if len(m.logs) > 20 {
			m.logs = m.logs[len(m.logs)-20:]
		}

	case errorMsg:
		m.logs = append(m.logs, errorStyle.Render(fmt.Sprintf("❌ Erro: %v", msg.err)))
		if len(m.logs) > 15 {
			m.logs = m.logs[1:]
		}

	default:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *model) updateSetList(filter string) {
	items := []list.Item{}
	for _, set := range m.sets {
		if filter == "" || strings.Contains(strings.ToLower(set.Code), strings.ToLower(filter)) ||
			strings.Contains(strings.ToLower(set.Name), strings.ToLower(filter)) ||
			strings.Contains(strings.ToLower(set.SetType), strings.ToLower(filter)) {
			items = append(items, setItem{set: set})
		}
	}
	m.setList.SetItems(items)
}

func (m model) View() string {
	switch m.state {
	case menuState:
		return m.renderMenu()
	case setSearchState:
		return m.renderSetSearch()
	case setDownloadState:
		return m.renderSetDownloadInput()
	case cardDownloadState:
		return m.renderCardInput()
	case setListState:
		return m.renderDownload()
	case configState:
		return m.renderConfig()
	default:
		return "Estado desconhecido"
	}
}

func (m model) renderMenu() string {
	s := titleStyle.Render("🎴 MTG Card Downloader") + "\n\n"
	for i, option := range m.menuOptions {
		cursor := " "
		if m.currentMenu == i {
			cursor = selectedStyle.Render("▶")
		}
		s += fmt.Sprintf("%s %s\n", cursor, option)
	}
	s += "\n" + helpStyle.Render("↑/↓: navegar • enter: selecionar • q: sair")
	return s
}

func (m model) renderSetSearch() string {
	s := titleStyle.Render("📋 Listar/Buscar Sets") + "\n\n"
	if len(m.sets) == 0 {
		s += m.spinner.View() + " Carregando sets...\n\n"
	} else {
		s += "🔍 Buscar: " + m.searchInput.View() + "\n\n"
		s += m.setList.View()
		s += "\n" + infoStyle.Render(fmt.Sprintf("Total: %d sets | Mostrando: %d", len(m.sets), len(m.setList.Items())))
	}
	s += "\n" + helpStyle.Render("digite para buscar • esc: voltar")
	return s
}

func (m model) renderSetDownloadInput() string {
	s := titleStyle.Render("🎴 Download por Set(s)") + "\n\n"
	s += "Digite os códigos dos sets:\n" + m.textInput.View() + "\n\n"
	s += infoStyle.Render("💡 Dicas:") + "\n"
	s += "  • Sets únicos: dom\n  • Múltiplos sets: dom,war,m21\n  • Todos os sets: ALL\n\n"

	if len(m.sets) > 0 {
		s += infoStyle.Render("🆕 Sets mais recentes:") + "\n"
		for i, set := range m.sets[:min(5, len(m.sets))] {
			digital := ""
			if set.Digital {
				digital = " 💻"
			}
			s += fmt.Sprintf("  %s - %s%s (%d cartas)\n", strings.ToUpper(set.Code), set.Name, digital, set.CardCount)
			if i >= 4 {
				break
			}
		}
		s += "\n"
	}

	s += warningStyle.Render("⚠️ 'ALL' baixará TODOS os sets (pode demorar muito!)") + "\n\n"
	s += helpStyle.Render("enter: confirmar • esc: voltar")
	return s
}

func (m model) renderCardInput() string {
	s := titleStyle.Render("🃏 Download por Carta") + "\n\n"
	s += "Digite o nome da carta:\n" + m.textInput.View() + "\n\n"
	s += infoStyle.Render("💡 Exemplo: Lightning Bolt, Black Lotus, Jace, etc.") + "\n\n"
	s += helpStyle.Render("enter: confirmar • esc: voltar")
	return s
}

func (m model) renderDownload() string {
	s := titleStyle.Render("📥 Download em Progresso") + "\n\n"

	current := atomic.LoadInt64(&m.completedTasks)
	total := atomic.LoadInt64(&m.totalTasks)

	if total > 0 {
		percent := float64(current) / float64(total)
		s += fmt.Sprintf("Progresso: %d/%d (%.1f%%)\n", current, total, percent*100)
		s += m.progress.View() + "\n\n"
	} else {
		s += m.spinner.View() + " Preparando download...\n\n"
	}

	if len(m.logs) > 0 {
		s += infoStyle.Render("📋 Log de Atividades:") + "\n"
		startIndex := 0
		if len(m.logs) > 12 {
			startIndex = len(m.logs) - 12
		}

		for i := startIndex; i < len(m.logs); i++ {
			log := m.logs[i]
			if strings.HasPrefix(log, "✅") || strings.HasPrefix(log, "✓") {
				s += successStyle.Render(log) + "\n"
			} else if strings.HasPrefix(log, "❌") || strings.HasPrefix(log, "✗") {
				s += errorStyle.Render(log) + "\n"
			} else if strings.HasPrefix(log, "🚀") || strings.HasPrefix(log, "🎉") {
				s += warningStyle.Render(log) + "\n"
			} else {
				s += log + "\n"
			}
		}
	}

	s += "\n" + helpStyle.Render("esc: voltar ao menu")
	return s
}

func (m model) renderConfig() string {
	s := titleStyle.Render("⚙️ Configurações") + "\n\n"

	options := []string{
		fmt.Sprintf("📁 Pasta de Download: %s", m.downloadDir),
		fmt.Sprintf("🎨 Qualidade: %s", m.quality),
		fmt.Sprintf("⚡ Workers: %d", m.maxWorkers),
		"🔙 Voltar",
	}

	for i, option := range options {
		cursor := " "
		if m.currentMenu == i {
			cursor = selectedStyle.Render("▶")
		}
		s += fmt.Sprintf("%s %s\n", cursor, option)
	}

	if m.textInput.Focused() {
		s += "\n" + infoStyle.Render("Digite o novo valor:") + "\n" + m.textInput.View()
	}

	s += "\n\n" + infoStyle.Render("💡 Dicas:") + "\n"
	s += "  • Pasta: Use caminho completo (ex: C:\\MinhasCartas)\n"
	s += "  • Qualidade: small (menor), normal (média), large (alta)\n"
	s += "  • Workers: Número de downloads simultâneos (1-50)\n\n"

	if len(m.logs) > 0 && m.currentMenu < 3 {
		s += infoStyle.Render("📋 Últimas alterações:") + "\n"
		startIndex := len(m.logs) - 3
		if startIndex < 0 {
			startIndex = 0
		}
		for i := startIndex; i < len(m.logs); i++ {
			s += m.logs[i] + "\n"
		}
		s += "\n"
	}

	s += helpStyle.Render("↑/↓: navegar • enter: editar • esc: voltar")
	return s
}

func (m model) fetchSetsCmd() tea.Cmd {
	return func() tea.Msg {
		sets, err := m.downloader.fetchSets()
		if err != nil {
			return errorMsg{err}
		}
		return setListMsg(sets)
	}
}

func (m model) downloadMultipleSetsCmd(setCodes []string) tea.Cmd {
	return func() tea.Msg {
		var completed, failed []string
		var allTasks []func() error

		for _, setCode := range setCodes {
			setCode = strings.TrimSpace(strings.ToLower(setCode))

			var targetSet *Set
			for _, set := range m.sets {
				if strings.EqualFold(set.Code, setCode) {
					targetSet = &set
					break
				}
			}

			if targetSet == nil {
				failed = append(failed, setCode)
				continue
			}

			cards, err := m.downloader.fetchSetCards(targetSet.SearchURI)
			if err != nil {
				failed = append(failed, setCode)
				continue
			}

			for _, card := range cards {
				tasks := m.downloader.processCard(card)
				allTasks = append(allTasks, tasks...)
			}
		}

		atomic.StoreInt64(&m.totalTasks, int64(len(allTasks)))
		atomic.StoreInt64(&m.completedTasks, 0)

		if len(allTasks) == 0 {
			return downloadCompleteMsg{success: false, message: "Nenhuma tarefa para executar", completed: []string{}, failed: setCodes}
		}

		semaphore := make(chan struct{}, m.maxWorkers)
		var wg sync.WaitGroup
		var successCount int64

		for _, task := range allTasks {
			wg.Add(1)
			go func(t func() error) {
				defer wg.Done()
				semaphore <- struct{}{}
				defer func() { <-semaphore }()

				if err := t(); err == nil {
					atomic.AddInt64(&successCount, 1)
				}
				atomic.AddInt64(&m.completedTasks, 1)
			}(task)
		}

		wg.Wait()

		for _, setCode := range setCodes {
			setCode = strings.TrimSpace(strings.ToLower(setCode))
			var targetSet *Set
			for _, set := range m.sets {
				if strings.EqualFold(set.Code, setCode) {
					targetSet = &set
					break
				}
			}
			if targetSet != nil {
				completed = append(completed, setCode)
			}
		}

		successMsg := fmt.Sprintf("Download finalizado: %d imagens processadas", successCount)
		if len(setCodes) > 1 {
			successMsg = fmt.Sprintf("Download de %d sets finalizado: %d imagens processadas", len(setCodes), successCount)
		}

		return downloadCompleteMsg{
			success:   successCount > 0,
			message:   successMsg,
			completed: completed,
			failed:    failed,
		}
	}
}

func (m model) downloadCardCmd(cardName string) tea.Cmd {
	return func() tea.Msg {
		card, err := m.downloader.fetchCard(cardName)
		if err != nil {
			return errorMsg{err}
		}

		prints, err := m.downloader.fetchSetCards(card.PrintsSearchURI)
		if err != nil {
			return errorMsg{err}
		}

		var allTasks []func() error
		for _, print := range prints {
			tasks := m.downloader.processCard(print)
			allTasks = append(allTasks, tasks...)
		}

		atomic.StoreInt64(&m.totalTasks, int64(len(allTasks)))
		atomic.StoreInt64(&m.completedTasks, 0)

		semaphore := make(chan struct{}, m.maxWorkers)
		var wg sync.WaitGroup
		var successCount int64

		for _, task := range allTasks {
			wg.Add(1)
			go func(t func() error) {
				defer wg.Done()
				semaphore <- struct{}{}
				defer func() { <-semaphore }()

				if err := t(); err == nil {
					atomic.AddInt64(&successCount, 1)
				}
				atomic.AddInt64(&m.completedTasks, 1)
			}(task)
		}

		wg.Wait()

		successMsg := fmt.Sprintf("✅ %d/%d imagens baixadas para '%s'", successCount, len(allTasks), card.Name)
		return downloadCompleteMsg{
			success:   successCount > 0,
			message:   successMsg,
			completed: []string{card.Name},
			failed:    []string{},
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Erro: %v", err)
		os.Exit(1)
	}
}

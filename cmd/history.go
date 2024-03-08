/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/andrewstuart/openai"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type histitem struct {
	file string
	date time.Time
	sess *openai.ChatSession
}

func (histitem histitem) FilterValue() string {
	val := histitem.file
	for _, m := range histitem.sess.Messages {
		val += "\n" + m.Content
	}
	return val
}

func (histitem histitem) Title() string {
	return histitem.file
}
func (histitem histitem) Description() string {
	return histitem.sess.Messages[0].Content
}

type histModel struct {
	sess *openai.ChatSession

	cvToggle bool
	// search textinput.Model
	list list.Model
	cv   viewport.Model
}

const listWidth = 40

func (m *histModel) rm(i histitem) error {
	p := viper.GetString("history.path")
	err := os.Remove(path.Join(p, i.file))
	if err != nil {
		return fmt.Errorf("could not remove history file: %w", err)
	}
	idx := m.list.Index()
	err = m.loadList()
	if err != nil {
		return fmt.Errorf("could not reload history list: %w", err)
	}
	m.list.Select(idx)
	return nil
}

func (m *histModel) loadList() error {
	p := viper.GetString("history.path")
	fs, err := os.ReadDir(p)

	if err != nil {
		return fmt.Errorf("could not read history directory: %w", err)
	}
	items := make([]list.Item, 0, len(fs))
	for _, f := range fs {
		ext := path.Ext(f.Name())
		if ext != ".json" {
			continue
		}
		fi, err := f.Info()
		if fi.Size() == 0 {
			continue
		}

		hi := histitem{file: f.Name()}
		t, err := time.Parse(time.RFC3339, f.Name())
		if err == nil {
			hi.date = t
		}
		f, err := os.OpenFile(path.Join(p, f.Name()), os.O_RDONLY, 0640)
		if err != nil {
			return fmt.Errorf("could not open history file: %w", err)
		}
		defer f.Close()
		err = json.NewDecoder(f).Decode(&hi.sess)
		if err != nil {
			return fmt.Errorf("could not decode history file: %w", err)
		}

		items = append(items, hi)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].(histitem).date.Before(items[j].(histitem).date)
	})
	// if len(items) > maxItems {
	// 	items = items[len(items)-maxItems:]
	// }
	d := list.NewDefaultDelegate()

	if m.list.Items() == nil {
		m.list = list.New(items, d, listWidth, 50)
		m.list.Title = "Chat History"
	} else {
		m.list.SetItems(items)
	}
	// m.list.KeyMap.PrevPage = key.NewBinding(key.WithKeys("left"))
	// m.list.KeyMap.NextPage = key.NewBinding(key.WithKeys("right"))
	return nil
}

func newHistModel() (histModel, error) {
	var m histModel
	err := m.loadList()
	if err != nil {
		return m, err
	}
	m.cv = viewport.New(100, 100)
	return m, nil
}

// Init is the first function that will be called. It returns an optional
// initial command. To not perform an initial command return nil.
func (m histModel) Init() tea.Cmd {
	return nil
}

// Update is called when a message is received. Use it to inspect messages
// and, in response, update the model and/or send a command.
func (m histModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.cv.Width = msg.Width - listWidth - cvStyle.GetBorderLeftSize()*2
		m.cv.Height = msg.Height - cvStyle.GetBorderTopSize()*2
		m.list.SetHeight(msg.Height - listStyle.GetBorderTopSize()*2)
		promptStyle = promptStyle.Width(m.cv.Width - promptStyle.GetBorderLeftSize()*2 - 2)
	case tea.KeyMsg:
		switch msg.String() {
		case "tab":
			m.cvToggle = !m.cvToggle
		case "ctrl+c", "q":
			return m, tea.Quit
		case "delete":
			item, ok := m.list.SelectedItem().(histitem)
			if ok {
				err := m.rm(item)
				if err != nil {
					log.Println(err)
				}
			}
		}
	}

	var cmd tea.Cmd
	if m.cvToggle {
		m.cv, cmd = m.cv.Update(msg)
	} else {
		m.list, cmd = m.list.Update(msg)
		item, ok := m.list.SelectedItem().(histitem)
		if ok {
			m.updateChatModel(item.sess)
		}
	}

	return m, cmd
}

var (
	promptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF00FF")).Border(lipgloss.RoundedBorder())
	listStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF00FF")).Border(lipgloss.RoundedBorder())
	cvStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF00FF")).Border(lipgloss.RoundedBorder())
)

func (m *histModel) updateChatModel(s *openai.ChatSession) (tea.Model, tea.Cmd) {
	vp := ""
	r, err := glamour.NewTermRenderer(glamour.WithWordWrap(m.cv.Width), glamour.WithAutoStyle(), glamour.WithPreservedNewLines())
	if err != nil {
		log.Println(err)
		return m, tea.Quit
	}
	for _, msg := range s.Messages[1:] {
		msg.Content = strings.ReplaceAll(msg.Content, "\t", "  ")
		switch msg.Role {
		case "system":
			vp += sysStyle.Render("Assistant: ")
			out, err := r.Render(strings.TrimSpace(msg.Content))
			if err != nil {
				log.Println(err)
				return m, tea.Quit
			}
			vp += out
		case "user":
			vp += userStyle.Render("You: ")
			out, err := r.Render(strings.TrimSpace(msg.Content))
			if err != nil {
				log.Println(err)
				return m, tea.Quit
			}
			vp += out
		}
	}
	m.cv.SetContent(vp)
	return m, nil
}

// View renders the program's UI, which is just a string. The view is
// rendered after every Update.
func (m histModel) View() string {
	item, ok := m.list.SelectedItem().(histitem)
	pr := ""
	if ok {
		pr = promptStyle.Render(item.sess.Messages[0].Content) + "\n"
		// m.r(item.sess)
	}

	ls, cv := listStyle, cvStyle
	if m.cvToggle {
		cv = cv.Copy().BorderStyle(lipgloss.DoubleBorder())
	} else {
		ls = ls.Copy().BorderStyle(lipgloss.DoubleBorder())
	}

	h := lipgloss.Height(pr)
	m.cv.Height -= h - 1

	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		ls.Render(m.list.View()),
		cv.Render(pr+m.cv.View()),
		cv.Render(m.cv.View()),
	)

}

// historyCmd represents the history command
var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "Chat history",
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newHistModel()
		if err != nil {
			return err
		}
		_, err = tea.NewProgram(m).Run()
		return err
	},
}

func init() {
	chatCmd.AddCommand(historyCmd)
}

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path"
	"strings"
	"time"

	"github.com/andrewstuart/openai"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/exp/maps"
)

var msg tea.Msg

type model struct {
	viewport viewport.Model
	textarea textarea.Model
	latest   string

	linesIdx  int
	lines     []string
	linesFile *os.File
	lastInput string

	program *tea.Program
	session *openai.ChatSession
}

func (m model) Close() error {
	return m.linesFile.Close()
}

func (m *model) readLines(ctx context.Context) error {
	p := viper.GetString("history.path")
	histPath := path.Join(p, "lines.txt")
	fmt.Println(histPath)

	f, err := os.OpenFile(histPath, os.O_RDWR|os.O_CREATE, 0640)
	if err != nil {
		panic(err)
	}
	m.linesFile = f

	_, err = f.Seek(0, 0)
	if err != nil {
		panic(err)
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		m.lines = append(m.lines, strings.TrimSpace(scanner.Text()))
	}
	m.linesIdx = len(m.lines)
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func newModel(sess *openai.ChatSession) *model {
	m := model{
		viewport: viewport.New(80, 50),
		textarea: textarea.New(),
		session:  sess,
	}

	m.readLines(ctx)

	m.textarea.SetWidth(80)
	m.textarea.SetHeight(3)
	m.textarea.ShowLineNumbers = false
	m.textarea.Focus()
	m.textarea.FocusedStyle.CursorLine = lipgloss.NewStyle()

	m.viewport.Style = lipgloss.NewStyle().Border(lipgloss.NormalBorder())
	m.viewport.KeyMap.Down.SetEnabled(false)
	m.viewport.KeyMap.Up.SetEnabled(false)
	m.viewport.KeyMap.PageDown.SetEnabled(false)
	m.viewport.KeyMap.PageUp.SetEnabled(false)
	m.viewport.KeyMap.HalfPageDown.SetEnabled(false)
	m.viewport.KeyMap.HalfPageUp.SetEnabled(false)

	return &m
}

// Init is the first function that will be called. It returns an optional
// initial command. To not perform an initial command return nil.
func (m model) Init() tea.Cmd {
	return nil
}

type chatUpdate string

var (
	userStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	sysStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#55FFFF"))
)

func (m model) updateLines(direction int) (tea.Model, tea.Cmd) {
	m.linesIdx += direction
	if m.linesIdx < 0 {
		m.linesIdx = 0
	}
	// At or past the end of the list
	if m.linesIdx > len(m.lines) {
		m.linesIdx = len(m.lines)
	}

	if direction < 0 {
		if m.linesIdx == len(m.lines)-1 {
			m.lastInput = m.textarea.Value()
		}
		m.textarea.SetValue(m.lines[m.linesIdx])
	} else {
		if m.linesIdx == len(m.lines) {
			m.textarea.SetValue(m.lastInput)
		} else {
			m.textarea.SetValue(m.lines[m.linesIdx])
		}
	}

	return m, nil
}

func (m model) updateViewport() (tea.Model, tea.Cmd) {
	vp := ""
	r, err := glamour.NewTermRenderer(glamour.WithWordWrap(m.viewport.Width), glamour.WithAutoStyle())
	if err != nil {
		log.Println(err)
		return m, tea.Quit
	}
	for _, msg := range m.session.Messages[1:] {
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
	if m.latest != "" && m.latest != m.session.Messages[len(m.session.Messages)-1].Content {
		vp += sysStyle.Render("Assistant: ")
		out, err := r.Render(m.latest)
		if err != nil {
			log.Println(err)
			return m, tea.Quit
		}
		vp += out
	}
	m.viewport.SetContent(vp)
	m.viewport.GotoBottom()

	return m, nil
}

// Update is called when a message is received. Use it to inspect messages
// and, in response, update the model and/or send a command.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// var vCmd, tCmd tea.Cmd
	// m.viewport, vCmd = m.viewport.Update(msg)
	var tCmd tea.Cmd
	m.textarea, tCmd = m.textarea.Update(msg)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 3
		m.textarea.SetWidth(msg.Width)
		// m.textarea.SetHeight(3)
		m.updateViewport()
	case bool:
		m.latest = ""
		return m.updateViewport()
	case chatUpdate:
		m.latest += string(msg)
		return m.updateViewport()
	case tea.KeyMsg:
		switch msg.String() {
		case "up":
			m, cmd := m.updateLines(-1)
			return m, tea.Batch(tCmd, cmd)
		case "down":
			m, cmd := m.updateLines(1)
			return m, tea.Batch(tCmd, cmd)
		case "ctrl+u":
			m.viewport.SetYOffset(m.viewport.YOffset - 1)
		case "ctrl+d":
			m.viewport.SetYOffset(m.viewport.YOffset + 1)
		case "pageup":
			m.viewport.SetYOffset(m.viewport.YOffset - m.viewport.Height)
		case "pagedown":
			m.viewport.SetYOffset(m.viewport.YOffset + m.viewport.Height)
		case "enter":
			val := strings.TrimSpace(m.textarea.Value())
			if val == "" {
				return m, nil
			}

			m.lines = append(m.lines, val)
			m.linesFile.WriteString(val + "\n")

			out, err := m.session.Stream(ctx, val)
			if err != nil {
				log.Println(err)
				return m, tea.Quit
			}

			// now stream updates from the chat output and update the chat messages output in a way tea can handle
			// TODO: this is a bit of a hack, but it works for now
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case msg, ok := <-out:
						if !ok {
							m.program.Send(true)
							return
						}
						m.program.Send(chatUpdate(msg))
					}
				}
			}()

			m.textarea.Reset()
			m.updateViewport()
		case "ctrl+c", "esc":
			return m, tea.Quit
		}
	}

	return m, tCmd
}

// View renders the program's UI, which is just a string. The view is
// rendered after every Update.
func (m model) View() string {
	return fmt.Sprintf("%s\n%s", m.viewport.View(), m.textarea.View())
}

// chatCmd represents the chat command
var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Chat with somebody",
	RunE: func(cmd *cobra.Command, args []string) error {

		prompt := "You are a helpful AI assistant."
		// fn := "Assistant"
		p := viper.GetString("history.path")
		var out *os.File

		if p != "" {
			fp := path.Join(p, time.Now().Format(time.RFC3339)) + ".json"
			var err error
			out, err = os.OpenFile(fp, os.O_RDWR|os.O_TRUNC|os.O_CREATE, 0600)
			if err != nil {
				return err
			}
			defer out.Close()
		}

		personality, _ := cmd.Flags().GetString("personality")
		if personality != "" {
			// fn = strings.Fields(personality)[0]
			prompt = "You answer in the speaking style of " + personality + "."
		}

		if pr, _ := cmd.Flags().GetString("prompt"); pr != "" {
			prompt = pr
			// fn = "Response"
		}

		prompts := viper.GetStringMapString("prompts")
		if prompts != nil && prompts[prompt] != "" {
			prompt = prompts[prompt]
		}

		sess := c.NewChatSession(prompt)
		m, _ := cmd.Flags().GetString("model")
		models, err := c.Models(ctx)
		if err != nil {
			return err
		}
		if models.Has(m) {
			sess.Model = m
		} else {
			sess.Model = openai.ChatModelGPT35Turbo0301
		}
		temp, _ := cmd.Flags().GetFloat64("temp")
		sess.Tpl.Temperature = &temp

		mod := newModel(&sess)
		defer mod.Close()
		prog := tea.NewProgram(mod)
		mod.program = prog

		_, err = prog.Run()
		if err != nil {
			return fmt.Errorf("error running program: %w", err)
		}
		if out != nil && len(sess.Messages) > 1 {
			out.Seek(0, 0)
			json.NewEncoder(out).Encode(sess)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(chatCmd)
	chatCmd.Flags().StringP("prompt", "p", "", "A prompt to override the default")
	chatCmd.Flags().String("personality", "", "Shorthand for a personality to use as the speaking style for the prompt.")
	chatCmd.Flags().String("model", openai.ChatModelGPT40215TurboPreview, "The model to use for chat completion")
	chatCmd.Flags().Float64P("temp", "t", 0, "The temperature to use for chat completion")

	chatCmd.RegisterFlagCompletionFunc("model", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		ourModels := []string{openai.ChatModelGPT35Turbo, openai.ChatModelGPT35Turbo0301, openai.ChatModelGPT35Turbo0613, openai.ChatModelGPT35Turbo0613}
		ms, err := c.Models(ctx)
		if err != nil {
			return ourModels, 0
		}
		if ms.Has(openai.ChatModelGPT4) {
			ourModels = append(ourModels, openai.ChatModelGPT4, openai.ChatModelGPT40314, openai.ChatModelGPT40613, openai.ChatModelGPT4TurboPreview)
		}
		if ms.Has(openai.ChatModelGPT432K) {
			ourModels = append(ourModels, openai.ChatModelGPT432K, openai.ChatModelGPT432K0314)
		}
		return ourModels, 0
	})

	chatCmd.RegisterFlagCompletionFunc("prompt", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		m := viper.GetStringMapString("prompts")
		if m != nil {
			return maps.Keys(m), 0
		}

		return []string{}, cobra.ShellCompDirectiveDefault
	})

}

// Copyright (C) 2026 Techdelight BV

package completions

import (
	"fmt"

	"github.com/techdelight/daedalus/core"
)

// Generate prints a shell completion script.
func Generate(cfg *core.Config) error {
	switch cfg.CompletionShell {
	case "bash":
		fmt.Print(bashCompletion)
	case "zsh":
		fmt.Print(zshCompletion)
	case "fish":
		fmt.Print(fishCompletion)
	default:
		return fmt.Errorf("usage: daedalus completion <bash|zsh|fish>")
	}
	return nil
}

const bashCompletion = `# bash completion for daedalus
# Add to ~/.bashrc: eval "$(daedalus completion bash)"

_daedalus() {
    local cur prev words cword
    _init_completion || return

    local subcommands="list prune remove rename config tui web completion skills personas"
    local flags="--build --target --resume -p --no-tmux --debug --dind --display --force --port --host --no-color --runner --persona --help -h"

    # Complete subcommands and flags for the first argument
    if [[ ${cword} -eq 1 ]]; then
        COMPREPLY=($(compgen -W "${subcommands} ${flags}" -- "${cur}"))
        return
    fi

    # Complete flags after subcommand
    case "${words[1]}" in
        remove|rename|config)
            # Try to complete project names from registry
            local projects
            projects=$(daedalus list 2>/dev/null | tail -n +3 | awk '{print $1}')
            COMPREPLY=($(compgen -W "${projects} ${flags}" -- "${cur}"))
            ;;
        completion)
            COMPREPLY=($(compgen -W "bash zsh fish" -- "${cur}"))
            ;;
        web)
            COMPREPLY=($(compgen -W "--port --host ${flags}" -- "${cur}"))
            ;;
        skills)
            COMPREPLY=($(compgen -W "add remove show" -- "${cur}"))
            ;;
        personas)
            COMPREPLY=($(compgen -W "list show create remove" -- "${cur}"))
            ;;
        *)
            COMPREPLY=($(compgen -W "${flags}" -- "${cur}"))
            ;;
    esac
}

complete -F _daedalus daedalus
`

const zshCompletion = `#compdef daedalus
# zsh completion for daedalus
# Add to ~/.zshrc: eval "$(daedalus completion zsh)"

_daedalus() {
    local -a subcommands flags

    subcommands=(
        'list:List all registered projects'
        'prune:Remove registry entries with missing directories'
        'remove:Remove named projects from the registry'
        'rename:Rename a registered project'
        'config:View or edit per-project default flags'
        'tui:Interactive dashboard for managing projects'
        'web:Web UI dashboard'
        'completion:Print shell completion script'
        'skills:Manage shared skill catalog'
        'personas:Manage named persona configurations'
    )

    flags=(
        '--build[Force rebuild the Docker image]'
        '--target[Build target stage]:stage:(dev godot base utils)'
        '--resume[Resume a previous Claude session]:session_id:'
        '-p[Run a headless single-prompt task]:prompt:'
        '--no-tmux[Run without tmux session wrapping]'
        '--debug[Enable Claude Code debug mode]'
        '--dind[Mount Docker socket]'
        '--display[Forward host display into container]'
        '--force[Force deletion in non-interactive mode]'
        '--no-color[Disable colored output]'
        '--port[Port for web UI]:port:'
        '--host[Host for web UI]:host:'
        '--help[Show help message]'
        '-h[Show help message]'
        '--runner[AI runner to use]:runner:(claude copilot)'
        '--persona[Persona configuration to use]:persona:'
        '--set[Set a default flag]:key=value:'
        '--unset[Remove a default flag]:key:'
    )

    _arguments -s \
        '1: :->cmd' \
        '*:: :->args'

    case $state in
        cmd)
            _describe -t subcommands 'subcommand' subcommands
            _describe -t flags 'flag' flags
            ;;
        args)
            case ${words[1]} in
                completion)
                    _values 'shell' bash zsh fish
                    ;;
                remove|rename|config)
                    local projects
                    projects=(${(f)"$(daedalus list 2>/dev/null | tail -n +3 | awk '{print $1}')"})
                    _describe -t projects 'project' projects
                    _describe -t flags 'flag' flags
                    ;;
                skills)
                    _values 'action' add remove show
                    ;;
                personas)
                    _values 'action' list show create remove
                    ;;
                *)
                    _describe -t flags 'flag' flags
                    ;;
            esac
            ;;
    esac
}

_daedalus
`

const fishCompletion = `# fish completion for daedalus
# Add to ~/.config/fish/completions/daedalus.fish

# Subcommands
complete -c daedalus -n '__fish_use_subcommand' -a 'list' -d 'List all registered projects'
complete -c daedalus -n '__fish_use_subcommand' -a 'prune' -d 'Remove registry entries with missing directories'
complete -c daedalus -n '__fish_use_subcommand' -a 'remove' -d 'Remove named projects from the registry'
complete -c daedalus -n '__fish_use_subcommand' -a 'rename' -d 'Rename a registered project'
complete -c daedalus -n '__fish_use_subcommand' -a 'config' -d 'View or edit per-project default flags'
complete -c daedalus -n '__fish_use_subcommand' -a 'tui' -d 'Interactive dashboard for managing projects'
complete -c daedalus -n '__fish_use_subcommand' -a 'web' -d 'Web UI dashboard'
complete -c daedalus -n '__fish_use_subcommand' -a 'completion' -d 'Print shell completion script'
complete -c daedalus -n '__fish_use_subcommand' -a 'skills' -d 'Manage shared skill catalog'
complete -c daedalus -n '__fish_use_subcommand' -a 'personas' -d 'Manage named persona configurations'

# Global flags
complete -c daedalus -l build -d 'Force rebuild the Docker image'
complete -c daedalus -l target -d 'Build target stage' -r -a 'dev godot base utils'
complete -c daedalus -l resume -d 'Resume a previous Claude session' -r
complete -c daedalus -s p -d 'Run a headless single-prompt task' -r
complete -c daedalus -l no-tmux -d 'Run without tmux session wrapping'
complete -c daedalus -l debug -d 'Enable Claude Code debug mode'
complete -c daedalus -l dind -d 'Mount Docker socket'
complete -c daedalus -l display -d 'Forward host display into container'
complete -c daedalus -l force -d 'Force deletion in non-interactive mode'
complete -c daedalus -l no-color -d 'Disable colored output'
complete -c daedalus -l port -d 'Port for web UI' -r
complete -c daedalus -l host -d 'Host for web UI' -r
complete -c daedalus -l help -d 'Show help message'
complete -c daedalus -s h -d 'Show help message'
complete -c daedalus -l runner -d 'AI runner to use' -r -a 'claude copilot'
complete -c daedalus -l persona -d 'Persona configuration to use' -r
complete -c daedalus -l set -d 'Set a default flag (key=value)' -r
complete -c daedalus -l unset -d 'Remove a default flag' -r

# Completion subcommand
complete -c daedalus -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'

# Skills subcommand
complete -c daedalus -n '__fish_seen_subcommand_from skills' -a 'add remove show'

# Personas subcommand
complete -c daedalus -n '__fish_seen_subcommand_from personas' -a 'list show create remove'

# Dynamic project names for remove and config
complete -c daedalus -n '__fish_seen_subcommand_from remove rename config' -a '(daedalus list 2>/dev/null | tail -n +3 | string match -r "^\S+")'
`

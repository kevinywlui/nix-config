# Enable Powerlevel10k instant prompt. Should stay close to the top of ~/.zshrc.
if [[ -r "${XDG_CACHE_HOME:-$HOME/.cache}/p10k-instant-prompt-${(%):-%n}.zsh" ]]; then
  source "${XDG_CACHE_HOME:-$HOME/.cache}/p10k-instant-prompt-${(%):-%n}.zsh"
fi

source $HOME/.zplug/init.zsh

# Plugins
zplug "zsh-users/zsh-autosuggestions", defer:2
zplug "zsh-users/zsh-syntax-highlighting", defer:3
zplug "romkatv/powerlevel10k", as:theme, depth:1

if ! zplug check --verbose; then
    printf "Install? [y/N]: "
    if read -q; then
        echo; zplug install
    fi
fi

zplug load

eval "$(zoxide init zsh)"

# Keybindings
bindkey -e
bindkey '^ ' autosuggest-accept
bindkey "^[[A" history-beginning-search-backward
bindkey "^[[B" history-beginning-search-forward

# Aliases
alias ls='ls --color'
alias vim='nvim'

# st FOO: ssh into the t480 and attach to tmux session FOO, creating it if absent.
# `new-session -A` attaches when the session exists and creates it otherwise;
# `ssh -t` forces a PTY so tmux gets a real terminal. SSH multiplexing (see
# ~/.ssh/config) keeps repeated calls fast.
st() {
  if [[ -z "$1" ]]; then
    echo "usage: st <session>" >&2
    return 2
  fi
  ssh -t t480 "tmux new-session -A -s ${(q)1}"
}

# History
HISTSIZE=100000
HISTFILE=~/.zsh_history
SAVEHIST=100000
HISTDUP=erase
setopt appendhistory
setopt sharehistory
setopt incappendhistory  # write each command immediately, not only on shell exit

# Path
export PATH=$PATH:$HOME/.local/bin

# Environment
export EDITOR='nvim'
export VISUAL='nvim'

# FZF
source <(fzf --zsh)

[[ ! -f ~/.p10k.zsh ]] || source ~/.p10k.zsh

# Load local overrides and extra configurations
if [[ -d "$HOME/.zshrc_includes" ]]; then
  for file in "$HOME/.zshrc_includes"/*.zsh(N); do
    source "$file"
  done
fi

# Zorven uzak terminal profili: .zshrc
# 1) Once kullanicinin kendi ~/.zshrc'si yuklenir (kurulumu aynen calisir).
# 2) Sonra yalnizca KULLANICININ AYARLAMADIGI seyler icin Zorven varsayilanlari eklenir.
# Kapatmak icin: ajani --terminal-plain-zsh ile baslatin.

if [[ -r ${ZORVEN_USER_ZDOTDIR:-$HOME}/.zshrc ]]; then
  ZDOTDIR=${ZORVEN_USER_ZDOTDIR:-$HOME}
  source "$ZDOTDIR/.zshrc"
  ZDOTDIR=$ZORVEN_ZDOTDIR
fi

[[ $TERM == dumb ]] && return 0

# --- Gecmis -----------------------------------------------------------------
if (( ${SAVEHIST:-0} == 0 )); then
  HISTFILE=${HISTFILE:-$ZORVEN_ZDOTDIR/history}
  HISTSIZE=100000
  SAVEHIST=100000
  setopt SHARE_HISTORY HIST_IGNORE_ALL_DUPS HIST_IGNORE_SPACE HIST_REDUCE_BLANKS HIST_SAVE_NO_DUPS EXTENDED_HISTORY
fi

# --- Tamamlama --------------------------------------------------------------
if (( ! ${+functions[compdef]} )); then
  autoload -Uz compinit
  compinit -C -d "$ZORVEN_ZDOTDIR/.zcompdump"
fi
if (( ${+functions[compdef]} )); then
  zstyle -L ':completion:*' menu >/dev/null 2>&1 || zstyle ':completion:*' menu select
  zstyle -L ':completion:*' matcher-list >/dev/null 2>&1 || \
    zstyle ':completion:*' matcher-list 'm:{a-zA-Z}={A-Za-z}' 'r:|[._-]=* r:|=*'
  zstyle -L ':completion:*' list-colors >/dev/null 2>&1 || zstyle ':completion:*' list-colors ${(s.:.)LS_COLORS}
fi
setopt AUTO_CD AUTO_PUSHD PUSHD_IGNORE_DUPS INTERACTIVE_COMMENTS COMPLETE_IN_WORD

# --- Tus baglari ------------------------------------------------------------
autoload -Uz up-line-or-beginning-search down-line-or-beginning-search
zle -N up-line-or-beginning-search
zle -N down-line-or-beginning-search
if [[ $(bindkey '^[[A') == *up-line-or-history* ]]; then bindkey '^[[A' up-line-or-beginning-search; fi
if [[ $(bindkey '^[OA') == *up-line-or-history* ]]; then bindkey '^[OA' up-line-or-beginning-search; fi
if [[ $(bindkey '^[[B') == *down-line-or-history* ]]; then bindkey '^[[B' down-line-or-beginning-search; fi
if [[ $(bindkey '^[OB') == *down-line-or-history* ]]; then bindkey '^[OB' down-line-or-beginning-search; fi
[[ $(bindkey '^[[H') == *undefined-key* ]] && bindkey '^[[H' beginning-of-line
[[ $(bindkey '^[[F') == *undefined-key* ]] && bindkey '^[[F' end-of-line
[[ $(bindkey '^[[3~') == *undefined-key* ]] && bindkey '^[[3~' delete-char
[[ $(bindkey '^[[1;5C') == *undefined-key* ]] && bindkey '^[[1;5C' forward-word
[[ $(bindkey '^[[1;5D') == *undefined-key* ]] && bindkey '^[[1;5D' backward-word

# --- Renkli takma adlar -----------------------------------------------------
if (( ! ${+aliases[ls]} )); then
  if ls --color=auto / >/dev/null 2>&1; then alias ls='ls --color=auto'; else alias ls='ls -G'; fi
fi
(( ${+aliases[ll]} )) || alias ll='ls -lah'
(( ${+aliases[la]} )) || alias la='ls -A'
(( ${+aliases[grep]} )) || alias grep='grep --color=auto'
(( ${+aliases[egrep]} )) || alias egrep='grep -E --color=auto'

# --- Git bilgili hizli istem ------------------------------------------------
if [[ -z $PROMPT || $PROMPT == '%m%# ' || $PROMPT == '%n@%m %1~ %# ' ]]; then
  autoload -Uz vcs_info add-zsh-hook
  zstyle ':vcs_info:*' enable git
  zstyle ':vcs_info:*' check-for-changes false
  zstyle ':vcs_info:git:*' formats ' %F{magenta}(%b)%f'
  zstyle ':vcs_info:git:*' actionformats ' %F{magenta}(%b|%a)%f'
  _zorven_precmd() { vcs_info }
  add-zsh-hook precmd _zorven_precmd
  setopt PROMPT_SUBST
  PROMPT='%(?..%F{red}[%?]%f )%F{green}%n@%m%f %F{blue}%~%f${vcs_info_msg_0_} %(!.#.$) '
fi

# --- Otomatik oneri ve sozdizimi vurgulama (gomulu, MIT / BSD-3) ------------
if [[ -z $ZORVEN_NO_PLUGINS ]]; then
  if (( ! ${+functions[_zsh_autosuggest_start]} )) && [[ -r $ZORVEN_ZDOTDIR/plugins/zsh-autosuggestions/zsh-autosuggestions.zsh ]]; then
    (( ${+ZSH_AUTOSUGGEST_STRATEGY} )) || ZSH_AUTOSUGGEST_STRATEGY=(history completion)
    source "$ZORVEN_ZDOTDIR/plugins/zsh-autosuggestions/zsh-autosuggestions.zsh"
  fi
  # Vurgulama eklentisi en SON yuklenmelidir.
  if (( ! ${+functions[_zsh_highlight]} )) && [[ -r $ZORVEN_ZDOTDIR/plugins/zsh-syntax-highlighting/zsh-syntax-highlighting.zsh ]]; then
    source "$ZORVEN_ZDOTDIR/plugins/zsh-syntax-highlighting/zsh-syntax-highlighting.zsh"
  fi
fi

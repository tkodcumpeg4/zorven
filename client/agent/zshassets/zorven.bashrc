# Zorven uzak terminal profili (bash --rcfile).
# Giris kabugu davranisini taklit eder (/etc/profile + kullanicinin profil dosyasi),
# kullanicinin ~/.bashrc'sini yukler, sonra yalnizca ayarlanmamis seyler icin
# hafif varsayilanlar ekler. Kullanicinin dosyalarina dokunulmaz.

[ -r /etc/profile ] && . /etc/profile
for __zv_f in "$HOME/.bash_profile" "$HOME/.bash_login" "$HOME/.profile"; do
  if [ -r "$__zv_f" ]; then
    . "$__zv_f"
    # Profil .bashrc'yi zaten yukluyorsa tekrar yukleme.
    if ! grep -q 'bashrc' "$__zv_f" 2>/dev/null; then
      [ -r "$HOME/.bashrc" ] && . "$HOME/.bashrc"
    fi
    __zv_loaded=1
    break
  fi
done
if [ -z "$__zv_loaded" ] && [ -r "$HOME/.bashrc" ]; then . "$HOME/.bashrc"; fi
unset __zv_f __zv_loaded

[ "$TERM" = dumb ] && return 0

# Gecmis
if [ -z "$HISTFILESIZE" ] || [ "$HISTFILESIZE" -lt 2000 ] 2>/dev/null; then
  HISTSIZE=100000
  HISTFILESIZE=100000
fi
case "$HISTCONTROL" in *erasedups*) ;; *) HISTCONTROL=ignoreboth:erasedups ;; esac
shopt -s histappend checkwinsize cmdhist 2>/dev/null

# Tamamlama
if ! declare -F _init_completion >/dev/null 2>&1; then
  for __zv_f in /usr/share/bash-completion/bash_completion /etc/bash_completion /opt/homebrew/etc/profile.d/bash_completion.sh /usr/local/etc/profile.d/bash_completion.sh; do
    if [ -r "$__zv_f" ]; then . "$__zv_f"; break; fi
  done
  unset __zv_f
fi

# Yukari/asagi: oneki metne gore gecmis arama (kullanici baglamadiysa)
case "$(bind -q history-search-backward 2>/dev/null)" in
  *"can be invoked via"*) ;;
  *) bind '"\e[A": history-search-backward' 2>/dev/null; bind '"\e[B": history-search-forward' 2>/dev/null
     bind '"\eOA": history-search-backward' 2>/dev/null; bind '"\eOB": history-search-forward' 2>/dev/null ;;
esac

# Renkli takma adlar
if ! alias ls >/dev/null 2>&1; then
  if ls --color=auto / >/dev/null 2>&1; then alias ls='ls --color=auto'; else alias ls='ls -G'; fi
fi
alias ll >/dev/null 2>&1 || alias ll='ls -lah'
alias la >/dev/null 2>&1 || alias la='ls -A'
alias grep >/dev/null 2>&1 || alias grep='grep --color=auto'

# Istem: kullanici/ana makine, dizin, git dali, cikis kodu
if [ -z "$PS1" ] || [ "$PS1" = '\s-\v\$ ' ] || [ "$PS1" = '\h:\W \u\$ ' ] || [ "$PS1" = '[\u@\h \W]\$ ' ]; then
  __zv_prompt() {
    local rc=$?
    local br
    br=$(git symbolic-ref --short -q HEAD 2>/dev/null) && br=" \[\e[35m\]($br)\[\e[0m\]" || br=""
    local st=""
    [ $rc -ne 0 ] && st="\[\e[31m\][$rc]\[\e[0m\] "
    PS1="${st}\[\e[32m\]\u@\h\[\e[0m\] \[\e[34m\]\w\[\e[0m\]${br} \\$ "
  }
  PROMPT_COMMAND="__zv_prompt${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
fi

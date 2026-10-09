# Zorven uzak terminal profili: .zprofile (kullanicininkini yukler)
if [[ -r ${ZORVEN_USER_ZDOTDIR:-$HOME}/.zprofile ]]; then
  ZDOTDIR=${ZORVEN_USER_ZDOTDIR:-$HOME}
  source "$ZDOTDIR/.zprofile"
  ZDOTDIR=$ZORVEN_ZDOTDIR
fi

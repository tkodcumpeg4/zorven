# Zorven uzak terminal profili: .zshenv
# Kullanicinin kendi dosyalarina DOKUNULMAZ; burasi onlari yukler.
# Ortam: ZORVEN_ZDOTDIR (bu dizin), ZORVEN_USER_ZDOTDIR (kullanicinin ZDOTDIR'i veya HOME).
if [[ -r ${ZORVEN_USER_ZDOTDIR:-$HOME}/.zshenv ]]; then
  ZDOTDIR=${ZORVEN_USER_ZDOTDIR:-$HOME}
  source "$ZDOTDIR/.zshenv"
  # Kullanici .zshenv'de ZDOTDIR'i kendi belirlediyse onu "kullanici dizini" say.
  if [[ $ZDOTDIR != ${ZORVEN_USER_ZDOTDIR:-$HOME} && $ZDOTDIR != $ZORVEN_ZDOTDIR ]]; then
    ZORVEN_USER_ZDOTDIR=$ZDOTDIR
    ZORVEN_USER_ZDOTDIR_SET=1
  fi
fi
ZDOTDIR=$ZORVEN_ZDOTDIR

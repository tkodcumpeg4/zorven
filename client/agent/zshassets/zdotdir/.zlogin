# Zorven uzak terminal profili: .zlogin (kullanicininkini yukler, ZDOTDIR'i geri verir)
if [[ -r ${ZORVEN_USER_ZDOTDIR:-$HOME}/.zlogin ]]; then
  ZDOTDIR=${ZORVEN_USER_ZDOTDIR:-$HOME}
  source "$ZDOTDIR/.zlogin"
fi
# Kullanicinin ortamini geri ver: alt kabuklar Zorven dizinini degil kullanicininkini gorsun.
if [[ -n $ZORVEN_USER_ZDOTDIR_SET ]]; then
  ZDOTDIR=$ZORVEN_USER_ZDOTDIR
else
  unset ZDOTDIR
fi
unset ZORVEN_USER_ZDOTDIR_SET

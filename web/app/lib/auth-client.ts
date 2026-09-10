import { createAuthClient } from "better-auth/vue"
import { organizationClient, twoFactorClient } from "better-auth/client/plugins"

export const authClient = createAuthClient({
  plugins: [
    organizationClient(),
    // Iki adimli dogrulama istemci eklentisi: authClient.twoFactor.* metotlarini
    // ve giriste twoFactorRedirect akisini saglar.
    twoFactorClient(),
  ],
})

export const {
  signIn,
  signUp,
  signOut,
  useSession,
  organization,
  twoFactor,
} = authClient

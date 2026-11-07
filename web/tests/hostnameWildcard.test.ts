// Calistirma: node --test web/tests/hostnameWildcard.test.ts
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { isWildcardDomainError } from '../app/lib/hostnameWildcard.ts'

test('sunucunun joker mesaji taninir', () => {
  const msg = 'joker alan adlari (*.alan.com) desteklenmiyor; her alt alan adini ayri ekleyin'
  assert.equal(isWildcardDomainError('invalid_domain', msg), true)
})

test('girilen deger * iceriyorsa joker sayilir', () => {
  assert.equal(isWildcardDomainError('invalid_domain', 'gecersiz alan adi formati', '*.sirket.com'), true)
})

test('baska invalid_domain hatalari ve baska kodlar jokersiz', () => {
  assert.equal(isWildcardDomainError('invalid_domain', 'gecersiz alan adi formati'), false)
  assert.equal(isWildcardDomainError('invalid_domain', undefined, 'api.sirket.com'), false)
  assert.equal(isWildcardDomainError('hostname_taken', 'joker'), false)
  assert.equal(isWildcardDomainError(undefined, 'joker'), false)
})

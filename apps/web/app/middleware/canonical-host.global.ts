export default defineNuxtRouteMiddleware(() => {
  const url = useRequestURL()
  if (url.hostname !== '127.0.0.1' && url.hostname !== '::1' && url.hostname !== '[::1]') {
    return
  }

  const target = new URL(url.toString())
  target.hostname = 'localhost'
  return navigateTo(target.toString(), { external: true, redirectCode: 302 })
})

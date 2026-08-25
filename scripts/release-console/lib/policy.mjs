const loopbackHost = '127.0.0.1'
const allowedActions = new Set(['status', 'build', 'deploy', 'stage-candidate', 'prepare-bluegreen', 'canary-green', 'return-blue', 'rollback'])

export function assertLoopbackHost(host) {
  if (host !== loopbackHost) throw new Error('发布控制台只能监听 127.0.0.1')
}

export function assertPublishableCandidate(candidate) {
  if (candidate?.status !== 'ready') throw new Error('候选尚未准备完成')
  if (!/^[a-f0-9]{64}$/i.test(candidate.sha256 ?? '')) throw new Error('候选 SHA256 无效')
}

export function isAllowedAction(action) {
  return allowedActions.has(action)
}

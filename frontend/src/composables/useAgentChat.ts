import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  ChatWithAgent,
  CreateAgentSession,
  DeleteAgentSession,
  GetAgentSessionList,
  GetAgentSessionMessages,
  GetAiConfigs,
  GetConfig,
  GetVersionInfo,
  SummarizeAgentSessionTitle,
} from '../services/app-api'
import { EventsOff, EventsOn } from '../../wailsjs/runtime'

type ChatMessage = {
  id: string
  avatar: string
  name: string
  datetime: string
  reasoning: string
  content: string
  role: 'assistant' | 'user'
  streaming?: boolean
}

type SessionItem = {
  sessionId: string
  title: string
  modelName?: string
  lastMessageAt?: string
  messageCount?: number
}

type TurnNavItem = {
  id: string
  idx: number
  title: string
}

export function useAgentChat() {
  const loading = ref(false)
  const inputValue = ref('')
  const isStreamLoad = ref(false)
  const chatRef = ref<any>(null)
  const chatSenderRef = ref<any>(null)
  const shouldAutoScroll = ref(true)
  const scrollBottomThresholdPx = 120
  const selectOptions = ref<any[]>([])
  const selectValue = ref<number | null>(null)
  const icon = ref('')

  const sidebarCollapsed = ref(true)
  const sessionList = ref<SessionItem[]>([])
  const activeSessionId = ref('')
  const chatList = ref<ChatMessage[]>([])
  const streamSessionId = ref('')
  const streamMessageId = ref('')

  const userAvatar = 'https://tdesign.gtimg.com/site/avatar.jpg'

  const activeSession = computed(() => sessionList.value.find((item) => item.sessionId === activeSessionId.value))
  const activeSessionTitle = computed(() => activeSession.value?.title || '新对话')
  const activeTurnIdx = ref(0)
  const turns = computed<TurnNavItem[]>(() => {
    const list = chatList.value.filter((m) => m.role === 'user')
    return list.map((m, idx) => ({
      id: m.id,
      idx,
      title: `第 ${idx + 1} 轮 · ${m.datetime || ''} · ${String(m.content || '').slice(0, 18)}`,
    }))
  })
  const turnIndexById = computed(() => {
    const m = new Map<string, number>()
    for (const t of turns.value) {
      m.set(t.id, t.idx)
    }
    return m
  })

  function getChatListEl(fromTarget?: HTMLElement | null): HTMLElement | null {
    if (fromTarget && fromTarget.classList?.contains('t-chat__list')) {
      return fromTarget
    }
    const root = chatRef.value?.$el as HTMLElement | undefined
    const el = (fromTarget?.querySelector?.('.t-chat__list') as HTMLElement | null) || null
    if (el) {
      return el
    }
    return (root?.querySelector?.('.t-chat__list') as HTMLElement | null) || null
  }

  function scrollToMsgId(msgId: string) {
    const listEl = getChatListEl(null)
    const target = listEl?.querySelector?.(`[data-msg-id="${msgId}"]`) as HTMLElement | null
    if (!target) {
      return
    }
    shouldAutoScroll.value = false
    target.scrollIntoView({ block: 'start', behavior: 'smooth' })
  }

  function jumpToTurn(idx: number) {
    const row = turns.value.find((t) => t.idx === idx)
    if (!row) {
      return
    }
    activeTurnIdx.value = idx
    scrollToMsgId(row.id)
  }

  function resolveSendText(val?: unknown): string {
    if (typeof val === 'string') {
      return val.trim()
    }
    if (val && typeof val === 'object') {
      const row = val as Record<string, unknown>
      const raw = row.inputValue ?? row.value ?? row.text ?? row.content
      if (typeof raw === 'string') {
        return raw.trim()
      }
    }
    return inputValue.value.trim()
  }

  function formatDateTime(v?: string) {
    if (!v) {
      return ''
    }
    const dt = new Date(v)
    if (Number.isNaN(dt.getTime())) {
      return v
    }
    const pad = (n: number) => String(n).padStart(2, '0')
    return `${dt.getMonth() + 1}-${pad(dt.getDate())} ${pad(dt.getHours())}:${pad(dt.getMinutes())}`
  }

  function formatSessionMeta(item: SessionItem) {
    const time = formatDateTime(item.lastMessageAt)
    if (item.modelName && time) {
      return `${item.modelName} · ${time}`
    }
    return item.modelName || time || '暂无消息'
  }

  function newMessageId() {
    return `msg-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  }

  function pushWelcomeMessage() {
    chatList.value = [
      {
        id: newMessageId(),
        avatar: icon.value,
        name: 'Go-Stock AI',
        datetime: '',
        reasoning: '',
        content: '我是你的 AI 股票分析助手。你可以直接问我个股逻辑、行业趋势、财报解读或交易计划。',
        role: 'assistant',
        streaming: false,
      },
    ]
  }

  function scrollToBottomSmooth() {
    nextTick(() => {
      if (chatRef.value?.scrollToBottom) {
        chatRef.value.scrollToBottom({ behavior: 'smooth' })
        return
      }
      const root = chatRef.value?.$el as HTMLElement | undefined
      const listEl = root?.querySelector?.('.t-chat__list') as HTMLElement | null
      if (listEl) {
        listEl.scrollTo({ top: listEl.scrollHeight, behavior: 'smooth' })
      }
    })
  }

  async function refreshSessions() {
    const list = await GetAgentSessionList()
    sessionList.value = (list || []).map((item: any) => ({
      sessionId: item.sessionId,
      title: item.title,
      modelName: item.modelName,
      lastMessageAt: item.lastMessageAt,
      messageCount: item.messageCount,
    }))
  }

  async function loadSessionMessages(sessionId: string) {
    const rows = await GetAgentSessionMessages(sessionId)
    const mapped = (rows || []).map((item: any) => ({
      id: `${item.ID || item.id || newMessageId()}`,
      avatar: item.role === 'assistant' ? icon.value : userAvatar,
      name: item.role === 'assistant' ? 'Go-Stock AI' : '用户',
      datetime: formatDateTime(item.CreatedAt || item.createdAt),
      reasoning: item.reasoning || '',
      content: item.content || '',
      role: item.role === 'assistant' ? 'assistant' : 'user',
      streaming: false,
    })) as ChatMessage[]

    mapped.sort((a, b) => {
      const at = new Date(a.datetime || '').getTime()
      const bt = new Date(b.datetime || '').getTime()
      if (!Number.isNaN(at) && !Number.isNaN(bt) && at !== bt) {
        return at - bt
      }
      return String(a.id).localeCompare(String(b.id))
    })
    chatList.value = mapped
    if (mapped.length === 0) {
      pushWelcomeMessage()
    }
    shouldAutoScroll.value = true
    scrollToBottomSmooth()
  }

  async function switchSession(sessionId: string) {
    if (!sessionId || sessionId === activeSessionId.value || isStreamLoad.value) {
      return
    }
    activeSessionId.value = sessionId
    await loadSessionMessages(sessionId)
  }

  async function handleCreateSession() {
    if (!selectValue.value) {
      return
    }
    const res = await CreateAgentSession(Number(selectValue.value))
    const sessionId = String(res?.sessionId || '').trim()
    if (!sessionId) {
      return
    }
    await refreshSessions()
    activeSessionId.value = sessionId
    pushWelcomeMessage()
  }

  async function removeSession(sessionId: string) {
    if (!sessionId || isStreamLoad.value) {
      return
    }
    await DeleteAgentSession(sessionId)
    if (activeSessionId.value === sessionId) {
      activeSessionId.value = ''
    }
    await refreshSessions()
    if (!activeSessionId.value && sessionList.value.length > 0) {
      activeSessionId.value = sessionList.value[0].sessionId
    }
    if (activeSessionId.value) {
      await loadSessionMessages(activeSessionId.value)
      return
    }
    pushWelcomeMessage()
  }

  const onStop = function () {
    loading.value = false
    isStreamLoad.value = false
    streamSessionId.value = ''
    streamMessageId.value = ''
    const row = chatList.value.find((x) => x.streaming)
    if (row) {
      row.streaming = false
    }
  }

  const inputEnter = async function (val?: unknown) {
    if (isStreamLoad.value) {
      return
    }
    const question = resolveSendText(val)
    if (!question) {
      return
    }

    if (!selectValue.value) {
      chatList.value = chatList.value.concat([
        {
          id: newMessageId(),
          avatar: icon.value,
          name: 'Go-Stock AI',
          datetime: formatDateTime(new Date().toISOString()),
          content: '请先在设置中配置并启用至少一个 AI 模型服务，然后再使用 AI 智能体。',
          reasoning: '',
          role: 'assistant',
          streaming: false,
        },
      ])
      return
    }

    if (!activeSessionId.value) {
      await handleCreateSession()
      if (!activeSessionId.value) {
        return
      }
    }

    const userMsg: ChatMessage = {
      id: newMessageId(),
      avatar: userAvatar,
      name: '用户',
      datetime: formatDateTime(new Date().toISOString()),
      content: question,
      reasoning: '',
      role: 'user',
      streaming: false,
    }
    const assistantPlaceholderId = newMessageId()
    const assistantMsg: ChatMessage = {
      id: assistantPlaceholderId,
      avatar: icon.value,
      name: 'Go-Stock AI',
      datetime: formatDateTime(new Date().toISOString()),
      content: '',
      reasoning: '',
      role: 'assistant',
      streaming: true,
    }

    chatList.value = chatList.value.concat([userMsg, assistantMsg])
    shouldAutoScroll.value = true
    loading.value = true
    isStreamLoad.value = true
    streamSessionId.value = activeSessionId.value
    streamMessageId.value = assistantPlaceholderId
    inputValue.value = ''
    scrollToBottomSmooth()
    ChatWithAgent(question, Number(selectValue.value), null, activeSessionId.value)
  }

  function clearConfirm() {
    if (!isStreamLoad.value) {
      handleCreateSession()
    }
  }

  function handleOperation() {
    // copy action handled by component internally
  }

  function handleChatScroll({ e }: any) {
    const rawTarget = e?.target as HTMLElement | undefined
    const listEl = getChatListEl(rawTarget || null)
    if (!listEl) {
      return
    }
    const distanceToBottom = listEl.scrollHeight - listEl.scrollTop - listEl.clientHeight
    const pinned = distanceToBottom <= scrollBottomThresholdPx
    shouldAutoScroll.value = pinned

    const anchors = Array.from(listEl.querySelectorAll('.msg-wrap[data-role="user"]')) as HTMLElement[]
    const topY = listEl.scrollTop + 12
    let activeId = ''
    for (const el of anchors) {
      if (el.offsetTop <= topY) {
        activeId = String(el.dataset.msgId || '')
      } else {
        break
      }
    }
    const idx = activeId ? turnIndexById.value.get(activeId) : undefined
    if (typeof idx === 'number') {
      activeTurnIdx.value = idx
    }
  }

  function onAgentMessage(data: any) {
    if (!isStreamLoad.value || streamSessionId.value !== activeSessionId.value) {
      return
    }

    const current = chatList.value.find((item) => item.id === streamMessageId.value) || chatList.value.find((item) => item.streaming)
    if (!current) {
      return
    }

    if (data?.role === 'assistant') {
      loading.value = false
      if (data.reasoning_content) {
        current.reasoning += data.reasoning_content
      }
      if (data.content) {
        current.content += data.content
      }
      if (Array.isArray(data.tool_calls)) {
        for (const tool of data.tool_calls) {
          const fn = tool?.function?.name || 'tool'
          const args = tool?.function?.arguments || '无'
          current.reasoning += '\n'
          current.reasoning += `\n\`\`\`${fn}\n参数：${args}\n\`\`\`\n`
        }
      }
      if (shouldAutoScroll.value) {
        scrollToBottomSmooth()
      }
    }

    if (data?.response_meta?.finish_reason) {
      isStreamLoad.value = false
      loading.value = false
      current.streaming = false
      const sid = activeSessionId.value
      streamSessionId.value = ''
      streamMessageId.value = ''
      refreshSessions().then(async () => {
        if (sid) {
          const title = await SummarizeAgentSessionTitle(sid)
          if (title) {
            await refreshSessions()
          }
        }
      })
    }
  }

  onMounted(async () => {
    EventsOff('agent-message')
    EventsOn('agent-message', onAgentMessage)

    const cfg = await GetConfig()
    if (cfg?.darkTheme) {
      document.documentElement.setAttribute('theme-mode', 'dark')
    } else {
      document.documentElement.removeAttribute('theme-mode')
    }

    const version = await GetVersionInfo()
    if (version?.icon) {
      icon.value = version.icon
    }

    const aiConfigs = await GetAiConfigs()
    selectOptions.value = aiConfigs || []
    selectValue.value = aiConfigs?.length ? aiConfigs[0].ID : null

    await refreshSessions()
    if (sessionList.value.length > 0) {
      activeSessionId.value = sessionList.value[0].sessionId
      await loadSessionMessages(activeSessionId.value)
    } else if (selectValue.value) {
      await handleCreateSession()
    } else {
      pushWelcomeMessage()
    }
  })

  onBeforeUnmount(() => {
    EventsOff('agent-message')
  })

  return {
    activeSessionId,
    activeSessionTitle,
    activeTurnIdx,
    chatList,
    chatRef,
    chatSenderRef,
    clearConfirm,
    formatSessionMeta,
    handleChatScroll,
    handleCreateSession,
    handleOperation,
    inputEnter,
    inputValue,
    isStreamLoad,
    jumpToTurn,
    loading,
    onStop,
    removeSession,
    selectOptions,
    selectValue,
    sessionList,
    sidebarCollapsed,
    switchSession,
    turns,
  }
}

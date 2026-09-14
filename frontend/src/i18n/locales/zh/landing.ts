export default {
  aiImage: {
    title: 'AI 生图',
    description: '使用自己的 API Key，通过 OpenAI 兼容接口批量生成并下载图片',
    concurrencyBadge: '固定并发 2',
    connection: {
      title: '连接配置',
      subtitle: '请求由当前浏览器直接发送',
      baseURL: 'Base URL',
      resetDefault: '恢复默认',
      model: '模型',
      apiKey: 'API Key',
      apiKeyPlaceholder: '请输入用于生图的 API Key',
      showKey: '显示 Key',
      hideKey: '隐藏 Key',
      rememberKey: '记住 Key',
      rememberKeyHint: '关闭时仅保留在当前页面内存',
      target: '请求目标',
      externalTarget: '外部请求目标'
    },
    prompts: {
      title: '提示词模板',
      selectedCount: '已选择 {count}/10',
      builtIn: '内置模板',
      custom: '自定义模板',
      addCustom: '新增',
      noCustom: '暂无自定义模板',
      edit: '编辑模板',
      delete: '删除模板'
    },
    composer: {
      promptPlaceholder: '描述你想生成的图像',
      sizeTier: '档位',
      ratio: '比例',
      count: '张数',
      standardGeneration: '普通生成',
      imageUnit: '张',
      customPromptName: '自定义 Prompt',
      copyName: '{name} · 第 {index} 张'
    },
    references: {
      title: '参考图（可选）',
      upload: '点击、拖拽或粘贴上传',
      hint: '最多 3 张参考图',
      remove: '移除参考图',
      clear: '清空'
    },
    ratios: {
      square: '方图 1:1',
      landscape: '横图 16:9',
      portrait: '竖图 9:16',
      wide: '宽幅 16:10',
      promptSuffix: '画面比例为 {ratio}，请严格按照该比例进行构图。'
    },
    workspace: {
      title: '图像生成',
      emptyTitle: '暂无图像',
      emptyDescription: '提交后会显示最终图片'
    },
    history: {
      title: '历史（本地缓存）',
      emptyTitle: '无历史记录',
      emptyDescription: '最终图片只保存在当前浏览器的本地缓存',
      collapse: '收起本地缓存',
      expand: '展开本地缓存',
      clear: '清空缓存',
      delete: '删除缓存图片'
    },
    builtInPrompts: {
      professionalAvatar: '专业头像',
      productPhoto: '商品主图',
      drinkPoster: '饮品海报',
      healingIllustration: '治愈插画',
      nightWallpaper: '夜空壁纸',
      astronautSticker: '宇航员贴纸'
    },
    generation: {
      start: '生成图片（{count}）',
      generating: '正在生成...',
      confirmTitle: '确认生成图片',
      confirmMessage: '即将向 {host} 发起 {count} 个独立生图请求。每个请求会产生一张图片并可能产生费用，是否继续？',
      confirmButton: '确认生成'
    },
    terminal: {
      title: '生成状态',
      ready: '工作台已就绪',
      idleDescription: '选择提示词并确认后，这里会显示每个任务的实时状态。',
      noTasks: '等待任务',
      taskSummary: '成功 {success} · 失败 {failed} · 运行 {running}',
      targetLog: '请求目标：{host}',
      modelLog: '使用模型：{model}',
      queuedLog: '已加入 {count} 个任务，并发数 {concurrency}',
      taskStarted: '开始生成：{name}',
      taskSucceeded: '生成完成：{name}',
      taskFailed: '生成失败：{name} · {reason}',
      completedLog: '本轮结束：成功 {success}，失败 {failed}',
      retryLog: '手动重试：{name}'
    },
    status: {
      queued: '等待中',
      running: '生成中',
      succeeded: '已完成',
      failed: '生成失败'
    },
    results: {
      title: '生成结果',
      summary: '共 {total} 个任务，已生成 {success} 张图片',
      clear: '清空结果',
      emptyTitle: '暂无生成结果',
      emptyDescription: '提交任务后，图片会按提示词选择顺序显示在这里。',
      preview: '预览图片',
      download: '下载图片',
      retry: '重试'
    },
    customDialog: {
      addTitle: '新增自定义模板',
      editTitle: '编辑自定义模板',
      name: '模板名称',
      namePlaceholder: '例如：品牌封面',
      prompt: '提示词',
      promptPlaceholder: '输入完整的生图提示词'
    },
    deleteDialog: {
      title: '删除自定义模板',
      message: '确定删除“{name}”吗？此操作不会影响已生成的结果。'
    },
    errors: {
      invalidBaseURL: '请输入有效的 HTTP 或 HTTPS Base URL。',
      insecureHTTP: '当前页面为 HTTPS，不能向非本机 HTTP 地址发送 API Key。',
      invalidKey: 'API Key 无效或没有生图权限，请检查后重试。',
      endpointNotFound: '未找到生图接口，请确认 Base URL 包含正确的 API 版本路径。',
      rateLimited: '请求过于频繁，接口已限流，请稍后手动重试。',
      serverError: '生图服务暂时异常，请稍后手动重试。',
      timeout: '请求超过五分钟未完成，已停止等待。',
      networkError: '网络请求失败，可能是网络中断、CORS 限制或目标不可达。',
      emptyResponse: '接口返回成功，但没有可用图片。',
      invalidResponse: '接口返回的图片数据格式无效。',
      requestFailed: '生图请求失败，请检查连接配置后重试。',
      aborted: '请求已取消。',
      maxSelectedPrompts: '最多只能选择 10 条提示词。',
      customPromptLimit: '最多保存 50 条自定义提示词。',
      referenceLimit: '最多只能上传 3 张参考图。',
      referenceType: '参考图仅支持 PNG、JPEG 或 WebP。',
      referenceSize: '单张参考图不能超过 20 MB。'
    }
  },
  batchImageGuide: {
    title: '图片批量生成',
    description: '一次提交多条提示词，任务完成后可统一下载图片结果'
  },
  // Home Page
  home: {
    viewOnGithub: '在 GitHub 上查看',
    viewDocs: '查看文档',
    docs: '文档',
    switchToLight: '切换到浅色模式',
    switchToDark: '切换到深色模式',
    dashboard: '控制台',
    login: '登录',
    getStarted: '立即开始',
    goToDashboard: '进入控制台',
    // 新增：面向用户的价值主张
    heroSubtitle: '一个密钥，畅用多个 AI 模型',
    heroDescription: '无需管理多个订阅账号，一站式接入 Claude、GPT、Gemini 等主流 AI 服务',
    tags: {
      subscriptionToApi: '订阅转 API',
      stickySession: '会话保持',
      realtimeBilling: '按量计费'
    },
    // 用户痛点区块
    painPoints: {
      title: '你是否也遇到这些问题？',
      items: {
        expensive: {
          title: '订阅费用高',
          desc: '每个 AI 服务都要单独订阅，每月支出越来越多'
        },
        complex: {
          title: '多账号难管理',
          desc: '不同平台的账号、密钥分散各处，管理起来很麻烦'
        },
        unstable: {
          title: '服务不稳定',
          desc: '单一账号容易触发限制，影响正常使用'
        },
        noControl: {
          title: '用量无法控制',
          desc: '不知道钱花在哪了，也无法限制团队成员的使用'
        }
      }
    },
    // 解决方案区块
    solutions: {
      title: '我们帮你解决',
      subtitle: '简单三步，开始省心使用 AI'
    },
    features: {
      unifiedGateway: '一键接入',
      unifiedGatewayDesc: '获取一个 API 密钥，即可调用所有已接入的 AI 模型，无需分别申请。',
      multiAccount: '稳定可靠',
      multiAccountDesc: '智能调度多个上游账号，自动切换和负载均衡，告别频繁报错。',
      balanceQuota: '用多少付多少',
      balanceQuotaDesc: '按实际使用量计费，支持设置配额上限，团队用量一目了然。'
    },
    // 优势对比
    comparison: {
      title: '为什么选择我们？',
      headers: {
        feature: '对比项',
        official: '官方订阅',
        us: '本平台'
      },
      items: {
        pricing: {
          feature: '付费方式',
          official: '固定月费，用不完也付',
          us: '按量付费，用多少付多少'
        },
        models: {
          feature: '模型选择',
          official: '单一服务商',
          us: '多模型随意切换'
        },
        management: {
          feature: '账号管理',
          official: '每个服务单独管理',
          us: '统一密钥，一站管理'
        },
        stability: {
          feature: '服务稳定性',
          official: '单账号易触发限制',
          us: '多账号池，自动切换'
        },
        control: {
          feature: '用量控制',
          official: '无法限制',
          us: '可设配额、查明细'
        }
      }
    },
    providers: {
      title: '已支持的 AI 模型',
      description: '一个 API，多种选择',
      supported: '已支持',
      soon: '即将推出',
      claude: 'Claude',
      gemini: 'Gemini',
      antigravity: 'Antigravity',
      more: '更多'
    },
    // CTA 区块
    cta: {
      title: '准备好开始了吗？',
      description: '注册即可获得免费试用额度，体验一站式 AI 服务',
      button: '免费注册'
    },
    footer: {
      allRightsReserved: '保留所有权利。'
    }
  },

  // Key Usage Query Page
  keyUsage: {
    title: 'API Key 用量查询',
    subtitle: '输入您的 API Key 以查看实时消费金额与使用状态',
    placeholder: 'sk-ant-mirror-xxxxxxxxxxxx',
    query: '查询',
    querying: '查询中...',
    privacyNote: '您的 Key 仅在浏览器本地处理，不会被存储',
    dateRange: '统计范围:',
    dateRangeToday: '今日',
    dateRange7d: '7 天',
    dateRange30d: '30 天',
    dateRange90d: '90 天',
    dateRangeCustom: '自定义',
    apply: '应用',
    used: '已使用',
    detailInfo: '详细信息',
    tokenStats: 'Token 统计',
    dailyDetail: '按日明细',
    modelStats: '模型用量统计',
    // Table headers
    date: '日期',
    model: '模型',
    requests: '请求数',
    inputTokens: '输入 Tokens',
    outputTokens: '输出 Tokens',
    cacheCreationTokens: '缓存创建',
    cacheReadTokens: '缓存读取',
    cacheWriteTokens: '缓存写入',
    totalTokens: '总 Tokens',
    cost: '费用',
    // Status
    quotaMode: 'Key 限额模式',
    walletBalance: '钱包余额',
    // Ring card titles
    totalQuota: '总额度',
    limit5h: '5 小时限额',
    limitDaily: '日限额',
    limit7d: '7 天限额',
    limitWeekly: '周限额',
    limitMonthly: '月限额',
    // Detail rows
    remainingQuota: '剩余额度',
    expiresAt: '过期时间',
    todayExpires: '(今日到期)',
    daysLeft: '({days} 天)',
    usedQuota: '已用额度',
    resetNow: '即将重置',
    subscriptionType: '订阅类型',
    billingType: '计费方式',
    subscriptionExpires: '订阅到期',
    // Usage stat cells
    todayRequests: '今日请求',
    todayInputTokens: '今日输入',
    todayOutputTokens: '今日输出',
    todayTokens: '今日 Tokens',
    todayCacheCreation: '今日缓存创建',
    todayCacheRead: '今日缓存读取',
    todayCost: '今日费用',
    rpmTpm: 'RPM / TPM',
    totalRequests: '累计请求',
    totalInputTokens: '累计输入',
    totalOutputTokens: '累计输出',
    totalTokensLabel: '累计 Tokens',
    totalCacheCreation: '累计缓存创建',
    totalCacheRead: '累计缓存读取',
    totalCost: '累计费用',
    avgDuration: '平均耗时',
    // Messages
    enterApiKey: '请输入 API Key',
    querySuccess: '查询成功',
    queryFailed: '查询失败',
    queryFailedRetry: '查询失败，请稍后重试',
    noDailyUsage: '暂无按日用量数据',
  },

  // Setup Wizard
  setup: {
    title: 'Sub2API 安装向导',
    description: '配置您的 Sub2API 实例',
    database: {
      title: '数据库配置',
      description: '连接到您的 PostgreSQL 数据库',
      host: '主机',
      port: '端口',
      username: '用户名',
      password: '密码',
      databaseName: '数据库名称',
      sslMode: 'SSL 模式',
      passwordPlaceholder: '密码',
      ssl: {
        disable: '禁用',
        require: '要求',
        verifyCa: '验证 CA',
        verifyFull: '完全验证'
      }
    },
    redis: {
      title: 'Redis 配置',
      description: '连接到您的 Redis 服务器',
      host: '主机',
      port: '端口',
      username: '用户名（可选）',
      password: '密码（可选）',
      database: '数据库',
      usernamePlaceholder: '默认用户留空',
      passwordPlaceholder: '密码',
      enableTls: '启用 TLS',
      enableTlsHint: '连接 Redis 时使用 TLS（公共 CA 证书）'
    },
    admin: {
      title: '管理员账户',
      description: '创建您的管理员账户',
      email: '邮箱',
      password: '密码',
      confirmPassword: '确认密码',
      passwordPlaceholder: '至少 8 个字符',
      confirmPasswordPlaceholder: '确认密码',
      passwordMismatch: '密码不匹配'
    },
    ready: {
      title: '准备安装',
      description: '检查您的配置并完成安装',
      database: '数据库',
      redis: 'Redis',
      adminEmail: '管理员邮箱'
    },
    status: {
      testing: '测试中...',
      success: '连接成功',
      testConnection: '测试连接',
      installing: '安装中...',
      completeInstallation: '完成安装',
      completed: '安装完成！',
      redirecting: '正在跳转到登录页面...',
      restarting: '服务正在重启，请稍候...',
      timeout: '服务重启时间超出预期，请手动刷新页面。'
    }
  },

  // Common
}

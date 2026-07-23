export default {
  aiImage: {
    title: 'AI Images',
    description: 'Generate and download images through an OpenAI-compatible endpoint using your own API key',
    concurrencyBadge: 'Concurrency 2',
    connection: {
      title: 'Connection',
      subtitle: 'Requests are sent directly from this browser',
      baseURL: 'Base URL',
      resetDefault: 'Reset',
      model: 'Model',
      apiKey: 'API Key',
      apiKeyPlaceholder: 'Enter an API key with image access',
      showKey: 'Show key',
      hideKey: 'Hide key',
      rememberKey: 'Remember key',
      rememberKeyHint: 'When off, the key stays in page memory only',
      target: 'Request target',
      externalTarget: 'External request target'
    },
    prompts: {
      title: 'Prompt Templates',
      selectedCount: '{count}/10 selected',
      builtIn: 'Built-in templates',
      custom: 'Custom templates',
      addCustom: 'Add',
      noCustom: 'No custom templates',
      edit: 'Edit template',
      delete: 'Delete template'
    },
    composer: {
      promptPlaceholder: 'Describe the image you want to generate',
      sizeTier: 'Tier',
      ratio: 'Aspect ratio',
      count: 'Count',
      standardGeneration: 'Standard generation',
      imageUnit: 'image(s)',
      customPromptName: 'Custom Prompt',
      copyName: '{name} · Image {index}'
    },
    references: {
      title: 'Reference images (optional)',
      upload: 'Click, drag, or paste to upload',
      hint: 'Up to 3 reference images',
      remove: 'Remove reference image',
      clear: 'Clear'
    },
    ratios: {
      square: 'Square 1:1',
      landscape: 'Landscape 16:9',
      portrait: 'Portrait 9:16',
      wide: 'Wide 16:10',
      promptSuffix: 'Use a {ratio} canvas and compose the image strictly for that aspect ratio.'
    },
    workspace: {
      title: 'Image Generation',
      emptyTitle: 'No Image Yet',
      emptyDescription: 'The final image will appear here after submission'
    },
    history: {
      title: 'History (Local Cache)',
      emptyTitle: 'No History',
      emptyDescription: 'Final images are stored only in this browser cache',
      collapse: 'Collapse local cache',
      expand: 'Expand local cache',
      clear: 'Clear cache',
      delete: 'Delete cached image'
    },
    builtInPrompts: {
      professionalAvatar: 'Professional Portrait',
      productPhoto: 'Product Photo',
      drinkPoster: 'Drink Poster',
      healingIllustration: 'Cozy Illustration',
      nightWallpaper: 'Night Sky Wallpaper',
      astronautSticker: 'Astronaut Sticker'
    },
    generation: {
      start: 'Generate Images ({count})',
      generating: 'Generating...',
      confirmTitle: 'Confirm Image Generation',
      confirmMessage: 'This will send {count} separate image requests to {host}. Each request creates one image and may incur charges. Continue?',
      confirmButton: 'Generate'
    },
    terminal: {
      title: 'Generation Status',
      ready: 'Workbench ready',
      idleDescription: 'Per-task status will appear here after you select prompts and confirm.',
      noTasks: 'Waiting for tasks',
      taskSummary: '{success} success · {failed} failed · {running} running',
      targetLog: 'Request target: {host}',
      modelLog: 'Model: {model}',
      queuedLog: '{count} tasks queued with concurrency {concurrency}',
      taskStarted: 'Started: {name}',
      taskSucceeded: 'Completed: {name}',
      taskFailed: 'Failed: {name} · {reason}',
      completedLog: 'Run complete: {success} success, {failed} failed',
      retryLog: 'Manual retry: {name}'
    },
    status: {
      queued: 'Queued',
      running: 'Generating',
      succeeded: 'Completed',
      failed: 'Failed'
    },
    results: {
      title: 'Results',
      summary: '{total} tasks, {success} images generated',
      clear: 'Clear Results',
      emptyTitle: 'No Results Yet',
      emptyDescription: 'Generated images will appear here in prompt selection order.',
      preview: 'Preview image',
      download: 'Download image',
      retry: 'Retry'
    },
    customDialog: {
      addTitle: 'Add Custom Template',
      editTitle: 'Edit Custom Template',
      name: 'Template Name',
      namePlaceholder: 'For example: Brand Cover',
      prompt: 'Prompt',
      promptPlaceholder: 'Enter the complete image generation prompt'
    },
    deleteDialog: {
      title: 'Delete Custom Template',
      message: 'Delete “{name}”? Existing generated results will not be affected.'
    },
    errors: {
      invalidBaseURL: 'Enter a valid HTTP or HTTPS Base URL.',
      insecureHTTP: 'This HTTPS page cannot send an API key to a non-local HTTP address.',
      invalidKey: 'The API key is invalid or does not have image access.',
      endpointNotFound: 'The image endpoint was not found. Check the API version path in the Base URL.',
      rateLimited: 'The endpoint is rate limiting requests. Retry manually later.',
      serverError: 'The image service is temporarily unavailable. Retry manually later.',
      timeout: 'The request did not finish within five minutes.',
      networkError: 'The request failed due to the network, CORS policy, or an unreachable target.',
      emptyResponse: 'The endpoint succeeded but returned no usable image.',
      invalidResponse: 'The endpoint returned invalid image data.',
      requestFailed: 'The image request failed. Check the connection settings and retry.',
      aborted: 'The request was cancelled.',
      maxSelectedPrompts: 'You can select up to 10 prompts.',
      customPromptLimit: 'You can save up to 50 custom prompts.',
      referenceLimit: 'You can upload up to 3 reference images.',
      referenceType: 'Reference images must be PNG, JPEG, or WebP.',
      referenceSize: 'Each reference image must be 20 MB or smaller.'
    }
  },
  batchImageGuide: {
    title: 'Batch Image Generation',
    description: 'Submit multiple prompts in one job and download the generated images when complete'
  },
  // Home Page
  home: {
    viewOnGithub: 'View on GitHub',
    viewDocs: 'View Documentation',
    docs: 'Docs',
    switchToLight: 'Switch to Light Mode',
    switchToDark: 'Switch to Dark Mode',
    dashboard: 'Dashboard',
    login: 'Login',
    getStarted: 'Get Started',
    goToDashboard: 'Go to Dashboard',
    // User-focused value proposition
    heroSubtitle: 'One Key, All AI Models',
    heroDescription: 'No need to manage multiple subscriptions. Access Claude, GPT, Gemini and more with a single API key',
    tags: {
      subscriptionToApi: 'Subscription to API',
      stickySession: 'Session Persistence',
      realtimeBilling: 'Pay As You Go'
    },
    // Pain points section
    painPoints: {
      title: 'Sound Familiar?',
      items: {
        expensive: {
          title: 'High Subscription Costs',
          desc: 'Paying for multiple AI subscriptions that add up every month'
        },
        complex: {
          title: 'Account Chaos',
          desc: 'Managing scattered accounts and API keys across different platforms'
        },
        unstable: {
          title: 'Service Interruptions',
          desc: 'Single accounts hitting rate limits and disrupting your workflow'
        },
        noControl: {
          title: 'No Usage Control',
          desc: "Can't track where your money goes or limit team member usage"
        }
      }
    },
    // Solutions section
    solutions: {
      title: 'We Solve These Problems',
      subtitle: 'Three simple steps to stress-free AI access'
    },
    features: {
      unifiedGateway: 'One-Click Access',
      unifiedGatewayDesc: 'Get a single API key to call all connected AI models. No separate applications needed.',
      multiAccount: 'Always Reliable',
      multiAccountDesc: 'Smart routing across multiple upstream accounts with automatic failover. Say goodbye to errors.',
      balanceQuota: 'Pay What You Use',
      balanceQuotaDesc: 'Usage-based billing with quota limits. Full visibility into team consumption.'
    },
    // Comparison section
    comparison: {
      title: 'Why Choose Us?',
      headers: {
        feature: 'Comparison',
        official: 'Official Subscriptions',
        us: 'Our Platform'
      },
      items: {
        pricing: {
          feature: 'Pricing',
          official: 'Fixed monthly fee, pay even if unused',
          us: 'Pay only for what you use'
        },
        models: {
          feature: 'Model Selection',
          official: 'Single provider only',
          us: 'Switch between models freely'
        },
        management: {
          feature: 'Account Management',
          official: 'Manage each service separately',
          us: 'Unified key, one dashboard'
        },
        stability: {
          feature: 'Stability',
          official: 'Single account rate limits',
          us: 'Multi-account pool, auto-failover'
        },
        control: {
          feature: 'Usage Control',
          official: 'Not available',
          us: 'Quotas & detailed analytics'
        }
      }
    },
    providers: {
      title: 'Supported AI Models',
      description: 'One API, Multiple Choices',
      supported: 'Supported',
      soon: 'Soon',
      claude: 'Claude',
      gemini: 'Gemini',
      antigravity: 'Antigravity',
      more: 'More'
    },
    // CTA section
    cta: {
      title: 'Ready to Get Started?',
      description: 'Sign up now and get free trial credits to experience seamless AI access',
      button: 'Sign Up Free'
    },
    footer: {
      allRightsReserved: 'All rights reserved.'
    }
  },

  // Key Usage Query Page
  keyUsage: {
    title: 'API Key Usage',
    subtitle: 'Enter your API Key to view real-time spending and usage status',
    placeholder: 'sk-ant-mirror-xxxxxxxxxxxx',
    query: 'Query',
    querying: 'Querying...',
    privacyNote: 'Your Key is processed locally in the browser and will not be stored',
    dateRange: 'Date Range:',
    dateRangeToday: 'Today',
    dateRange7d: '7 Days',
    dateRange30d: '30 Days',
    dateRange90d: '90 Days',
    dateRangeCustom: 'Custom',
    apply: 'Apply',
    used: 'Used',
    detailInfo: 'Detail Information',
    tokenStats: 'Token Statistics',
    dailyDetail: 'Daily Detail',
    modelStats: 'Model Usage Statistics',
    // Table headers
    date: 'Date',
    model: 'Model',
    requests: 'Requests',
    inputTokens: 'Input Tokens',
    outputTokens: 'Output Tokens',
    cacheCreationTokens: 'Cache Creation',
    cacheReadTokens: 'Cache Read',
    cacheWriteTokens: 'Cache Write',
    totalTokens: 'Total Tokens',
    cost: 'Cost',
    // Status
    quotaMode: 'Key Quota Mode',
    walletBalance: 'Wallet Balance',
    // Ring card titles
    totalQuota: 'Total Quota',
    limit5h: '5-Hour Limit',
    limitDaily: 'Daily Limit',
    limit7d: '7-Day Limit',
    limitWeekly: 'Weekly Limit',
    limitMonthly: 'Monthly Limit',
    // Detail rows
    remainingQuota: 'Remaining Quota',
    expiresAt: 'Expires At',
    todayExpires: '(expires today)',
    daysLeft: '({days} days)',
    usedQuota: 'Used Quota',
    resetNow: 'Resetting soon',
    subscriptionType: 'Subscription Type',
    subscriptionExpires: 'Subscription Expires',
    // Usage stat cells
    todayRequests: 'Today Requests',
    todayInputTokens: 'Today Input',
    todayOutputTokens: 'Today Output',
    todayTokens: 'Today Tokens',
    todayCacheCreation: 'Today Cache Creation',
    todayCacheRead: 'Today Cache Read',
    todayCost: 'Today Cost',
    rpmTpm: 'RPM / TPM',
    totalRequests: 'Total Requests',
    totalInputTokens: 'Total Input',
    totalOutputTokens: 'Total Output',
    totalTokensLabel: 'Total Tokens',
    totalCacheCreation: 'Total Cache Creation',
    totalCacheRead: 'Total Cache Read',
    totalCost: 'Total Cost',
    avgDuration: 'Avg Duration',
    // Messages
    enterApiKey: 'Please enter an API Key',
    querySuccess: 'Query successful',
    queryFailed: 'Query failed',
    queryFailedRetry: 'Query failed, please try again later',
    noDailyUsage: 'No daily usage data',
  },

  // Setup Wizard
  setup: {
    title: 'Sub2API Setup',
    description: 'Configure your Sub2API instance',
    database: {
      title: 'Database Configuration',
      description: 'Connect to your PostgreSQL database',
      host: 'Host',
      port: 'Port',
      username: 'Username',
      password: 'Password',
      databaseName: 'Database Name',
      sslMode: 'SSL Mode',
      passwordPlaceholder: 'Password',
      ssl: {
        disable: 'Disable',
        require: 'Require',
        verifyCa: 'Verify CA',
        verifyFull: 'Verify Full'
      }
    },
    redis: {
      title: 'Redis Configuration',
      description: 'Connect to your Redis server',
      host: 'Host',
      port: 'Port',
      username: 'Username (optional)',
      password: 'Password (optional)',
      database: 'Database',
      usernamePlaceholder: 'Leave empty for default user',
      passwordPlaceholder: 'Password',
      enableTls: 'Enable TLS',
      enableTlsHint: 'Use TLS when connecting to Redis (public CA certs)'
    },
    admin: {
      title: 'Admin Account',
      description: 'Create your administrator account',
      email: 'Email',
      password: 'Password',
      confirmPassword: 'Confirm Password',
      passwordPlaceholder: 'Min 8 characters',
      confirmPasswordPlaceholder: 'Confirm password',
      passwordMismatch: 'Passwords do not match'
    },
    ready: {
      title: 'Ready to Install',
      description: 'Review your configuration and complete setup',
      database: 'Database',
      redis: 'Redis',
      adminEmail: 'Admin Email'
    },
    status: {
      testing: 'Testing...',
      success: 'Connection Successful',
      testConnection: 'Test Connection',
      installing: 'Installing...',
      completeInstallation: 'Complete Installation',
      completed: 'Installation completed!',
      redirecting: 'Redirecting to login page...',
      restarting: 'Service is restarting, please wait...',
      timeout: 'Service restart is taking longer than expected. Please refresh the page manually.'
    }
  },

  // Common
}

import { describe, expect, it } from 'vitest'
import { compareTestModelIds } from '../testModelDefaults'

describe('测试模型按版本号降序', () => {
  it.each([
    [['gpt-5.6-sol', 'gpt-6-astra', 'gpt-6-sol', 'gpt-6.1-sol'], ['gpt-6.1-sol', 'gpt-6-sol', 'gpt-6-astra', 'gpt-5.6-sol']],
    [['grok-4.5', 'grok-4.6', 'grok-4.7'], ['grok-4.7', 'grok-4.6', 'grok-4.5']],
    [['gemini-3.7-pro', 'gemini-4-pro', 'gemini-3.8-pro'], ['gemini-4-pro', 'gemini-3.8-pro', 'gemini-3.7-pro']],
    [['claude-sonnet-4', 'claude-opus-5', 'claude-opus-5.5'], ['claude-opus-5.5', 'claude-opus-5', 'claude-sonnet-4']],
    [['gpt-6.9-sol', 'gpt-6.10-sol'], ['gpt-6.10-sol', 'gpt-6.9-sol']],
    [['claude-sonnet-4-5-20260901', 'claude-opus-4.6'], ['claude-opus-4.6', 'claude-sonnet-4-5-20260901']],
    [['gpt-6-sol-2026-10-21', 'gpt-6.1-astra'], ['gpt-6.1-astra', 'gpt-6-sol-2026-10-21']],
    [['text-latest', 'gpt-6-sol'], ['gpt-6-sol', 'text-latest']],
    [['gpt-6-astra', 'claude-opus-6', 'gpt-6-sol'], ['gpt-6-sol', 'gpt-6-astra', 'claude-opus-6']],
    [['gpt-4-0613', 'gpt-4-32k', 'gpt-4.5'], ['gpt-4.5', 'gpt-4-0613', 'gpt-4-32k']],
    [['claude-sonnet-4-5-1m', 'claude-opus-4.6'], ['claude-opus-4.6', 'claude-sonnet-4-5-1m']],
  ])('排序 %j', (models, expected) => {
    expect([...models].sort(compareTestModelIds)).toEqual(expected)
  })

  it('无版本和相同版本的非 GPT 模型保持列表原顺序', () => {
    expect(compareTestModelIds('text-latest', 'text-default')).toBe(0)
    expect(compareTestModelIds('claude-sonnet-5', 'claude-opus-5')).toBe(0)
    expect(compareTestModelIds('claude-sonnet-4-5-1m', 'claude-sonnet-4.5')).toBe(0)
  })
})

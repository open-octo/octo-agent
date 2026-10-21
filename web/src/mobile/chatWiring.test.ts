import { describe, it, expect } from 'vitest'
import { echoRefs } from './chatWiring'

describe('echoRefs', () => {
  it('maps images to their data URL and other files to a pdf: chip ref', () => {
    expect(echoRefs([
      { name: 'shot.jpg', mime_type: 'image/jpeg', data_url: 'data:image/jpeg;base64,AAAA' },
      { name: 'report.pdf', mime_type: 'application/pdf', path: '/api/uploads/1_report.pdf' },
    ])).toEqual(['data:image/jpeg;base64,AAAA', 'pdf:report.pdf'])
  })

  it('is empty without files', () => {
    expect(echoRefs()).toEqual([])
    expect(echoRefs([])).toEqual([])
  })
})

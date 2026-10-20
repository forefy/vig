import createClient from 'openapi-react-query'
import { api } from './client'

// Typed React Query hooks over the generated client. Usage:
//   const { data } = $api.useQuery('get', '/v1/plans')
//   const m = $api.useMutation('post', '/v1/audits')
export const $api = createClient(api)

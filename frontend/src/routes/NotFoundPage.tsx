import { Link } from 'react-router'

import { Button } from '@/components/ui/button'

export default function NotFoundPage() {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 text-center">
      <p className="text-2xl font-semibold">404</p>
      <p className="text-muted-foreground">这个页面不存在</p>
      <Button asChild variant="outline" size="sm">
        <Link to="/">回首页</Link>
      </Button>
    </div>
  )
}

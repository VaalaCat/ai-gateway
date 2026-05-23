'use client';
import Link from 'next/link';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { KeyRound, BarChart3 } from 'lucide-react';
import { useBYOKChannels } from '@/lib/api/byok-channels';
import { useTranslations } from 'next-intl';

export function ProfileBYOKCard() {
  const t = useTranslations('byok.profileCard');
  const tByok = useTranslations('byok');
  const { data, isLoading } = useBYOKChannels({ page: 1, page_size: 1 });
  const count = data?.total ?? 0;
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <KeyRound className="h-5 w-5" />
          {t('title')}
        </CardTitle>
        <CardDescription>
          {isLoading
            ? tByok('loading')
            : count > 0
              ? t('descriptionCount', { count })
              : t('descriptionEmpty')}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex gap-2">
        <Button asChild>
          <Link href="/byok">{t('manage')}</Link>
        </Button>
        <Button asChild variant="outline">
          <Link href="/byok/stats"><BarChart3 className="h-4 w-4 mr-2" />{t('usage')}</Link>
        </Button>
      </CardContent>
    </Card>
  );
}

import { RoomDetailPage } from '../pages/RoomDetailPage';

export async function sendMessages(room: RoomDetailPage, prefix: string, count: number): Promise<void> {
  for (let i = 1; i <= count; i++) {
    await room.sendMessage(`${prefix} ${i}`);
  }
}

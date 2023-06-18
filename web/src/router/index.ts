import { createRouter, createWebHistory } from 'vue-router'
import HomeView from '../views/HomeView.vue'
import ChannelView from '../views/ChannelView.vue'
import MessageView from '../views/MessageView.vue'
import ConnView from '../views/ConnView.vue'
import ConversationView from '../views/ConversationView.vue'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: '/',
      name: 'home',
      component: HomeView
    },
    {
      path: '/channel',
      name: 'channel',
      component: ChannelView
    },
    {
      path: '/message',
      name: 'message',
      component: MessageView
    },
    {
      path: '/conn',
      name: 'conn',
      component: ConnView
    },
    {
      path: '/conversation',
      name: 'conversation',
      component: ConversationView
    },
  ]
})

export default router

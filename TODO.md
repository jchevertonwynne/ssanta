# TODO

- when a user deletes their account, it should send an event to the ws handling process so that chats can be updated accordingly. in a simialr manner check for other instacnes of where events should be sent out
- messages should be persisted in nowhere/seesion storage/local storage via a toggle. stored msgs should be encrypted. when new msgs come in we display them and also add them to the store
- we should be able to search messages in the UI. we should support literal searches, regex searches & filtering who send the message
- hide the name & time for msgs grouped together. maybe we can make messages copy on click rather than having an actual button. the message grouping should be within 10 minutes of each other for a user

- the invite user textbox should be a dropdown isntead. this should be dynamically updated as users create & delete accounts